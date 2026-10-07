package integration

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The proxy forwards the real Engine API and only interrupts an explicitly
// selected attach connection. Fixture observations and cleanup use the original
// Docker endpoint. No daemon response or helper result is fabricated.
type dockerFaultProxy struct {
	env      []string
	attached chan *faultAttachment
	dropped  atomic.Bool
}

type faultAttachment struct {
	client  net.Conn
	backend io.ReadWriteCloser
	once    sync.Once
}

func (a *faultAttachment) disconnect() {
	a.once.Do(func() {
		// A reset represents a broken connection rather than normal helper EOF.
		// Docker can legitimately wait for container exit after a clean EOF.
		if tcp, ok := a.client.(*net.TCPConn); ok {
			_ = tcp.SetLinger(0)
		}
		_ = a.client.Close()
		_ = a.backend.Close()
	})
}

type faultResponseWriter struct {
	http.ResponseWriter
	attached func(net.Conn)
}

func (w faultResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err == nil {
		w.attached(conn)
	}
	return conn, rw, err
}

type lostReply struct {
	io.ReadWriteCloser
	drop func()
}

func (r lostReply) Read(p []byte) (int, error) {
	n, err := r.ReadWriteCloser.Read(p)
	if n > 0 {
		r.drop()
		return 0, io.ErrUnexpectedEOF
	}
	return n, err
}

func (r lostReply) CloseWrite() error {
	// Preserve Docker's input EOF: the helper publishes only after the complete
	// input stream. Losing this half-close would test a broken proxy instead.
	c, ok := r.ReadWriteCloser.(interface{ CloseWrite() error })
	if !ok {
		return errors.New("upgraded Docker connection cannot half-close input")
	}
	return c.CloseWrite()
}

func newSocketFaultProxy(t *testing.T, socket string, loseReply bool) *dockerFaultProxy {
	t.Helper()
	p := &dockerFaultProxy{attached: make(chan *faultAttachment, 4)}
	transport := &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}
	var mu sync.Mutex
	var attachments []*faultAttachment
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxy := &httputil.ReverseProxy{
			Rewrite: func(r *httputil.ProxyRequest) {
				r.Out.URL.Scheme, r.Out.URL.Host, r.Out.Host = "http", "docker", "docker"
			},
			Transport: transport,
			ErrorLog:  log.New(io.Discard, "", 0),
		}
		if strings.HasSuffix(r.URL.Path, "/attach") {
			a := &faultAttachment{}
			proxy.ModifyResponse = func(response *http.Response) error {
				backend, ok := response.Body.(io.ReadWriteCloser)
				if response.StatusCode != http.StatusSwitchingProtocols || !ok {
					return fmt.Errorf("expected upgraded Docker attach connection, got %s", response.Status)
				}
				a.backend = backend
				if loseReply {
					response.Body = lostReply{ReadWriteCloser: backend, drop: func() {
						p.dropped.Store(true)
						a.disconnect()
					}}
				}
				return nil
			}
			w = faultResponseWriter{ResponseWriter: w, attached: func(conn net.Conn) {
				a.client = conn
				mu.Lock()
				attachments = append(attachments, a)
				mu.Unlock()
				p.attached <- a
			}}
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(func() {
		mu.Lock()
		for _, a := range attachments {
			a.disconnect()
		}
		mu.Unlock()
		server.Close()
		transport.CloseIdleConnections()
	})
	p.env = []string{"DOCKER_HOST=tcp://" + strings.TrimPrefix(server.URL, "http://"), "DOCKER_CONTEXT=", "DOCKER_TLS=", "DOCKER_TLS_VERIFY=", "DOCKER_CERT_PATH="}
	return p
}

func (f *dockerSession) faultProxy(loseReply bool) *dockerFaultProxy {
	f.t.Helper()
	// context inspect resolves Docker's own host/context precedence, including
	// Colima and DOCKER_HOST. The TCP frontend remains client-local in all cases.
	endpoint := strings.TrimSpace(string(f.docker("context", "inspect", "--format", "{{.Endpoints.docker.Host}}")))
	if !strings.HasPrefix(endpoint, "unix://") {
		f.t.Skipf("connection-fault fixture requires a Unix Docker endpoint, got %s", endpoint)
	}
	return newSocketFaultProxy(f.t, strings.TrimPrefix(endpoint, "unix://"), loseReply)
}

func TestDockerSessionDisconnectStopsRemoteHelper(t *testing.T) {
	f := newDockerSession(t, "12345", "23456", false, false)
	before, volumes := f.state(), f.volumes()
	source := filepath.Join(f.root, "disconnect source")
	want := writeSession(t, source, threadB, "retry after broken Docker connection")
	blocker := f.block(".hcorral-staging.lock")
	proxy := f.faultProxy(false)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := f.command(ctx, f.binary, "session", "import", threadB, source, "--format=json")
	cmd.Env = append(cmd.Env, proxy.env...)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	must(t, cmd.Start())
	done := make(chan struct{})
	var commandErr error
	go func() { commandErr = cmd.Wait(); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Error("launcher did not exit after connection fault")
		}
	})
	var attachment *faultAttachment
	select {
	case attachment = <-proxy.attached:
	case <-done:
		t.Fatalf("launcher exited before attach: %v\n%s", commandErr, &stderr)
	case <-time.After(30 * time.Second):
		t.Fatal("no Docker attach connection observed")
	}
	f.waitFor("running transfer helper", func() bool {
		ids := f.helperIDs()
		return len(ids) == 1 && strings.TrimSpace(string(f.docker("inspect", "--format", "{{.State.Running}}", ids[0]))) == "true"
	})
	attachment.disconnect()
	select {
	case <-done:
		assertUnknownTransfer(t, commandErr, out.String(), stderr.String())
	case <-time.After(25 * time.Second):
		t.Fatal("launcher did not finish after broken Docker connection")
	}
	f.assertPreserved(before, volumes)
	if strings.TrimSpace(string(f.docker("inspect", "--format", "{{.State.Running}}", blocker))) != "true" {
		t.Fatal("connection-fault cleanup stopped unrelated lock owner")
	}
	f.docker("stop", "--time", "1", blocker)
	result := f.transfer(nil, "import", threadB, source, "")
	got, _ := f.containerFile(".codex/" + result.Result.MainPath)
	if !bytes.Equal(got, want) {
		t.Fatal("retry after connection fault did not preserve complete history")
	}
	f.assertPreserved(before, volumes)
}

func TestDockerSessionLostCompletionIsRetryable(t *testing.T) {
	f := newDockerSession(t, "501", "20", false, false)
	before, volumes := f.state(), f.volumes()
	source := filepath.Join(f.root, "lost reply source")
	want := writeSession(t, source, threadB, "committed before reply was lost")
	proxy := f.faultProxy(true)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := f.command(ctx, f.binary, "session", "import", threadB, source, "--format=json")
	cmd.Env = append(cmd.Env, proxy.env...)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	err := cmd.Run()
	assertUnknownTransfer(t, err, out.String(), stderr.String())
	if !proxy.dropped.Load() {
		t.Fatal("test did not intercept the helper response")
	}
	// Inspect through the original endpoint before retrying: publication must
	// actually have happened even though the caller never received its result.
	got, header := f.containerFile(".codex/" + rolloutPath(threadB))
	if !bytes.Equal(got, want) || header.Uid != 501 || header.Gid != 20 || header.Mode&0o777 != 0o600 {
		t.Fatalf("lost reply was not preceded by complete private publication: %+v", header)
	}
	f.assertPreserved(before, volumes)
	result := f.transfer(nil, "import", threadB, source, "")
	for _, file := range result.Result.Files {
		if file.Created || file.Promoted || file.Prefix {
			t.Fatalf("retry did not reuse confirmed existing history: %+v", file)
		}
	}
	got, after := f.containerFile(".codex/" + result.Result.MainPath)
	if !bytes.Equal(got, want) || !after.ModTime.Equal(header.ModTime) || after.Uid != header.Uid || after.Gid != header.Gid || after.Mode != header.Mode {
		t.Fatal("retry changed already published history")
	}
	f.assertPreserved(before, volumes)
}

func assertUnknownTransfer(t *testing.T, err error, stdout, stderr string) {
	t.Helper()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 || stdout != "" ||
		(!strings.Contains(stderr, "without a confirmed completion result") && !strings.Contains(stderr, "publication status is unknown")) {
		t.Fatalf("wanted unconfirmed publication error, got %v stdout=%s stderr=%s", err, stdout, stderr)
	}
}

// Exercise the proxy's upgrade, input half-close and reply-loss mechanism even
// without Docker. This verifies test plumbing only; the tests above require an
// actual daemon, packaged launcher and embedded helper.
func TestDockerFaultProxyPreservesInputEOF(t *testing.T) {
	for _, loseReply := range []bool{false, true} {
		t.Run(fmt.Sprintf("lose-reply=%t", loseReply), func(t *testing.T) {
			// Keep Unix socket names short on macOS regardless of TMPDIR length.
			dir, err := os.MkdirTemp("/tmp", "hcorral-proxy-")
			must(t, err)
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			socket := filepath.Join(dir, "docker.sock")
			listener, err := net.Listen("unix", socket)
			must(t, err)
			t.Cleanup(func() { _ = listener.Close() })
			received := make(chan error, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					received <- err
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
				r := bufio.NewReader(conn)
				if _, err = http.ReadRequest(r); err != nil {
					received <- err
					return
				}
				if _, err = io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n"); err != nil {
					received <- err
					return
				}
				data, err := io.ReadAll(r)
				if err == nil && string(data) != "complete payload" {
					err = fmt.Errorf("wrong input: %q", data)
				}
				if err == nil {
					_, err = io.WriteString(conn, "confirmed publication")
				}
				received <- err
			}()
			proxy := newSocketFaultProxy(t, socket, loseReply)
			address := strings.TrimPrefix(proxy.env[0], "DOCKER_HOST=tcp://")
			conn, err := net.DialTimeout("tcp", address, 5*time.Second)
			must(t, err)
			defer conn.Close()
			must(t, conn.SetDeadline(time.Now().Add(10*time.Second)))
			_, err = io.WriteString(conn, "POST /v1.47/containers/example/attach HTTP/1.1\r\nHost: docker\r\nConnection: Upgrade\r\nUpgrade: tcp\r\nContent-Length: 0\r\n\r\n")
			must(t, err)
			r := bufio.NewReader(conn)
			response, err := http.ReadResponse(r, nil)
			must(t, err)
			if response.StatusCode != 101 {
				t.Fatalf("no upgrade: %s", response.Status)
			}
			_, err = io.WriteString(conn, "complete payload")
			must(t, err)
			must(t, conn.(*net.TCPConn).CloseWrite())
			data, readErr := io.ReadAll(r)
			must(t, <-received)
			if loseReply {
				if len(data) != 0 || readErr == nil || !proxy.dropped.Load() {
					t.Fatalf("reply not lost: %q %v", data, readErr)
				}
			} else if string(data) != "confirmed publication" || readErr != nil {
				t.Fatalf("reply did not survive input EOF: %q %v", data, readErr)
			}
		})
	}
}
