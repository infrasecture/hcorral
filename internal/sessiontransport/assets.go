package sessiontransport

import (
	"bytes"
	"compress/gzip"
	"embed"
	"fmt"
	"io"
	"io/fs"
)

//go:embed helpers/*
var helperAssets embed.FS

// ReadHelper returns a Linux helper built from this launcher's source. Both
// architectures are bundled, including in macOS launchers and native builds.
func ReadHelper(architecture string) ([]byte, error) {
	return readHelper(helperAssets, architecture)
}

func readHelper(assets fs.FS, architecture string) ([]byte, error) {
	if architecture != "amd64" && architecture != "arm64" {
		return nil, fmt.Errorf("unsupported session helper architecture %q", architecture)
	}
	compressed, err := fs.ReadFile(assets, "helpers/linux-"+architecture+".gz")
	if err != nil {
		return nil, fmt.Errorf("this launcher lacks its Linux %s session helper; build with ./build.sh or install a complete release: %w", architecture, err)
	}
	reader := bytes.NewReader(compressed)
	gz, err := gzip.NewReader(reader)
	if err != nil {
		return nil, fmt.Errorf("read bundled session helper: %w", err)
	}
	defer gz.Close()
	gz.Multistream(false)
	const maximum = 128 << 20
	data, err := io.ReadAll(io.LimitReader(gz, maximum+1))
	if err != nil {
		return nil, fmt.Errorf("decode bundled session helper: %w", err)
	}
	if len(data) == 0 || len(data) > maximum || reader.Len() != 0 {
		return nil, fmt.Errorf("bundled session helper has an invalid size or trailing data")
	}
	return data, nil
}
