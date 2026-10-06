package sessiontransport

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"strconv"

	"github.com/infrasecture/hcorral/internal/identity"
	"github.com/infrasecture/hcorral/internal/session"
	"github.com/infrasecture/hcorral/internal/sessionhelper"
)

// TransferOptions keeps client-host paths separate from container paths. The
// caller resolves effective SQLite configuration before invoking a transfer.
type TransferOptions struct {
	Operation           string
	ThreadID            string
	HostHome            string
	HostSQLiteHome      string
	ContainerSQLiteHome string
	Limits              session.Limits
}

type helperRun func(context.Context, []string, io.Reader, io.Writer) error

// Transfer joins the native host implementation to the supplied Linux helper.
// A nonempty result together with an error means publication was acknowledged,
// but finalization (for example remote cleanup) failed. An error alone must not
// be interpreted as proof that the destination was unchanged.
func (d Docker) Transfer(ctx context.Context, workspace identity.Workspace, target Target, options TransferOptions, stderr io.Writer) (session.Result, error) {
	if options.ContainerSQLiteHome != target.SQLiteHome {
		return session.Result{}, errors.New("effective SQLite home does not match the inspected storage selection")
	}
	remote := func(ctx context.Context, args []string, input io.Reader, output io.Writer) error {
		return d.Run(ctx, workspace, target, args, input, output, stderr)
	}
	return transfer(ctx, target.CodexHome, options, remote)
}

func transfer(ctx context.Context, containerHome string, options TransferOptions, remote helperRun) (session.Result, error) {
	if options.Operation != "export" && options.Operation != "import" {
		return session.Result{}, errors.New("session transfer requires export or import")
	}
	id, err := session.ParseID(options.ThreadID)
	if err != nil {
		return session.Result{}, err
	}
	if err := options.Limits.Validate(); err != nil {
		return session.Result{}, err
	}
	if options.Limits.RecordBytes > math.MaxInt64-options.Limits.ManifestBytes {
		return session.Result{}, errors.New("combined transfer result limits overflow")
	}
	if !filepath.IsAbs(options.HostHome) || !filepath.IsAbs(options.HostSQLiteHome) || !absolutePath(containerHome) || !absolutePath(options.ContainerSQLiteHome) {
		return session.Result{}, errors.New("session transfer requires resolved absolute Codex and SQLite homes at both endpoints")
	}
	if err := ctx.Err(); err != nil {
		return session.Result{}, err
	}
	if remote == nil {
		return session.Result{}, errors.New("session transfer has no remote endpoint")
	}
	options.ThreadID = id
	local := helperRun(sessionhelper.Run)
	producer, consumer := remote, local
	fromHome, fromSQLite := containerHome, options.ContainerSQLiteHome
	toHome, toSQLite := options.HostHome, options.HostSQLiteHome
	if options.Operation == "import" {
		producer, consumer = local, remote
		fromHome, fromSQLite, toHome, toSQLite = toHome, toSQLite, fromHome, fromSQLite
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	stop := context.AfterFunc(ctx, func() {
		reader.CloseWithError(ctx.Err())
		writer.CloseWithError(ctx.Err())
	})
	defer stop()
	sent := make(chan error, 1)
	go func() {
		err := producer(ctx, transferArgs("export", fromHome, fromSQLite, options), nil, writer)
		// The producer has released its writer locks before EOF is exposed.
		// Receive requires EOF before publication acquires destination locks:
		// even source/destination aliases cannot deadlock on our own locks.
		writer.CloseWithError(err)
		sent <- err
	}()
	// Source inspection and writer acquisition finish before the first payload
	// byte. Do not initialize a destination or start its helper for a source
	// that is absent, busy or invalid. This buffer remains bounded.
	buffered := bufio.NewReader(reader)
	if _, err := buffered.Peek(1); err != nil {
		cancel()
		reader.CloseWithError(err)
		return session.Result{}, fmt.Errorf("prepare session source: %w", errors.Join(err, <-sent))
	}
	response := &boundedBuffer{maximum: options.Limits.RecordBytes + options.Limits.ManifestBytes, description: "helper completion result"}
	receivedErr := consumer(ctx, transferArgs("import", toHome, toSQLite, options), buffered, response)
	// A consumer can reject an import before reading it. Unblock a producer
	// still writing and cancel its remote process before waiting for cleanup.
	reader.CloseWithError(receivedErr)
	if receivedErr != nil {
		cancel()
	}
	sentErr := <-sent
	result, resultErr := decodeResult(response.Bytes(), options)
	transportErr := errors.Join(sentErr, receivedErr)
	if transportErr != nil {
		if resultErr == nil {
			return result, fmt.Errorf("destination confirmed session publication, but transfer finalization failed: %w", transportErr)
		}
		return session.Result{}, fmt.Errorf("session transfer failed without a confirmed completion result: %w", transportErr)
	}
	if resultErr != nil {
		return session.Result{}, fmt.Errorf("destination completion result is invalid; publication status is unknown: %w", resultErr)
	}
	return result, nil
}

func transferArgs(operation, home, sqliteHome string, options TransferOptions) []string {
	return []string{operation, "--protocol=" + strconv.Itoa(session.ProtocolVersion), "--home", home, "--sqlite-home", sqliteHome,
		"--id", options.ThreadID, "--record-bytes=" + strconv.FormatInt(options.Limits.RecordBytes, 10),
		"--file-bytes=" + strconv.FormatInt(options.Limits.FileBytes, 10), "--files=" + strconv.Itoa(options.Limits.Files),
		"--manifest-bytes=" + strconv.FormatInt(options.Limits.ManifestBytes, 10)}
}

type boundedBuffer struct {
	buffer      bytes.Buffer
	maximum     int64
	description string
}

func (r *boundedBuffer) Bytes() []byte  { return r.buffer.Bytes() }
func (r *boundedBuffer) String() string { return r.buffer.String() }

func (r *boundedBuffer) Write(p []byte) (int, error) {
	if int64(len(p)) > r.maximum-int64(r.buffer.Len()) {
		return 0, fmt.Errorf("%s exceeds transfer limits", r.description)
	}
	return r.buffer.Write(p)
}

func decodeResult(data []byte, options TransferOptions) (session.Result, error) {
	var result session.Result
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return session.Result{}, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return session.Result{}, errors.New("extra data after completion result")
	}
	if result.ThreadID != options.ThreadID || result.Metadata.ThreadID != options.ThreadID || result.MainPath == "" || result.Destination == "" || len(result.Files) == 0 || len(result.Files) > options.Limits.Files {
		return session.Result{}, errors.New("completion result does not identify the requested conversation")
	}
	main := 0
	for _, file := range result.Files {
		if file.Extended && (file.Created || !file.Prefix) {
			return session.Result{}, errors.New("completion result claims an invalid prefix extension")
		}
		if _, err := session.ParseID(file.RolloutID); err != nil {
			return session.Result{}, err
		}
		if !file.Prefix {
			main++
			if file.Path != result.MainPath {
				return session.Result{}, errors.New("completion result has an inconsistent main rollout")
			}
		}
	}
	if main != 1 {
		return session.Result{}, errors.New("completion result must contain exactly one main rollout")
	}
	return result, nil
}
