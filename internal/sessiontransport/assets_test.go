package sessiontransport

import (
	"bytes"
	"compress/gzip"
	"context"
	"debug/elf"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"testing/fstest"
	"time"

	"github.com/infrasecture/hcorral/internal/session"
	"github.com/infrasecture/hcorral/internal/sessionhelper"
)

func compressedHelper(t *testing.T, data []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	w := gzip.NewWriter(&buffer)
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// Run against the generated payloads during release qualification. Ordinary
// source checks exercise the decoder without requiring generated executables.
func TestBundledHelpers(t *testing.T) {
	if os.Getenv("HCORRAL_TEST_BUNDLED_HELPERS") != "1" {
		t.Skip("set HCORRAL_TEST_BUNDLED_HELPERS=1 after building both helper payloads")
	}
	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			data, err := ReadHelper(arch)
			if err != nil {
				t.Fatal(err)
			}
			file, err := elf.NewFile(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			machine := elf.EM_X86_64
			if arch == "arm64" {
				machine = elf.EM_AARCH64
			}
			if file.Machine != machine || file.Type != elf.ET_EXEC {
				t.Fatal("embedded helper architecture/type mismatch")
			}
			for _, program := range file.Progs {
				if program.Type == elf.PT_INTERP || program.Type == elf.PT_DYNAMIC {
					t.Fatal("embedded helper is dynamically linked")
				}
			}
			if runtime.GOOS != "linux" || runtime.GOARCH != arch {
				return
			}
			path := filepath.Join(t.TempDir(), "session-helper")
			if err := os.WriteFile(path, data, 0o700); err != nil {
				t.Fatal(err)
			}
			output, err := exec.Command(path, "protocol").Output()
			if err != nil {
				t.Fatal(err)
			}
			var capabilities sessionhelper.Capabilities
			if err := json.Unmarshal(output, &capabilities); err != nil {
				t.Fatal(err)
			}
			if capabilities.Protocol != session.ProtocolVersion || capabilities.OS != "linux" || capabilities.Arch != arch {
				t.Fatalf("bundled protocol mismatch: %+v", capabilities)
			}
			t.Run("compatible-prefix-extension", func(t *testing.T) {
				testPrefixTransfers(t, func(ctx context.Context, args []string, input io.Reader, output io.Writer) error {
					cmd := exec.CommandContext(ctx, path, args...)
					cmd.WaitDelay = time.Second
					cmd.Stdin, cmd.Stdout = input, output
					cmd.Stderr = os.Stderr
					return cmd.Run()
				})
			})
			for _, operation := range []string{"export", "import"} {
				for _, alias := range []bool{false, true} {
					name := operation
					if alias {
						name += "/shared-storage"
					}
					t.Run(name, func(t *testing.T) {
						source, data := transferFixture(t)
						destination := filepath.Join(t.TempDir(), "destination")
						if alias {
							if err := os.Symlink(source, destination); err != nil {
								t.Fatal(err)
							}
						}
						host, container := destination, source
						if operation == "import" {
							host, container = source, destination
						}
						remote := func(ctx context.Context, args []string, input io.Reader, output io.Writer) error {
							cmd := exec.CommandContext(ctx, path, args...)
							cmd.WaitDelay = time.Second
							cmd.Stdin, cmd.Stdout = input, output
							var stderr bytes.Buffer
							cmd.Stderr = &stderr
							if err := cmd.Run(); err != nil {
								t.Logf("bundled helper: %s", stderr.String())
								return err
							}
							return nil
						}
						ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
						defer cancel()
						result, err := transfer(ctx, container, transferOptions(operation, host, container), remote)
						if err != nil || result.ThreadID != transferID {
							t.Fatalf("bundled helper transfer: %+v %v", result, err)
						}
						got, err := os.ReadFile(filepath.Join(destination, transferRollout))
						if err != nil || !bytes.Equal(got, data) {
							t.Fatalf("bundled helper changed history: %v", err)
						}
						noStaging(t, destination)
					})
				}
			}
		})
	}
}

func TestHelperAssetsDecodeExactlyOneCompletePayload(t *testing.T) {
	data := bytes.Repeat([]byte("helper executable bytes\x00"), 200)
	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			assets := fstest.MapFS{"helpers/linux-" + arch + ".gz": {Data: compressedHelper(t, data)}}
			got, err := readHelper(assets, arch)
			if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("payload: %v", err)
			}
		})
	}
	valid := compressedHelper(t, data)
	corrupt := append([]byte(nil), valid...)
	corrupt[len(corrupt)-8] ^= 1
	for name, encoded := range map[string][]byte{
		"missing": nil, "empty": compressedHelper(t, nil), "not gzip": data,
		"truncated": valid[:len(valid)-1], "checksum": corrupt,
		"trailing":     append(append([]byte(nil), valid...), 'x'),
		"concatenated": append(append([]byte(nil), valid...), valid...),
	} {
		t.Run(name, func(t *testing.T) {
			assets := fstest.MapFS{}
			if encoded != nil {
				assets["helpers/linux-amd64.gz"] = &fstest.MapFile{Data: encoded}
			}
			if _, err := readHelper(assets, "amd64"); err == nil {
				t.Fatal("accepted invalid payload")
			}
		})
	}
	if _, err := readHelper(fstest.MapFS{}, "riscv64"); err == nil {
		t.Fatal("accepted unsupported architecture")
	}
}
