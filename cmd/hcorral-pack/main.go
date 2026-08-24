package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type fileSpecs []string

func (s *fileSpecs) String() string         { return strings.Join(*s, ",") }
func (s *fileSpecs) Set(value string) error { *s = append(*s, value); return nil }

func main() {
	if len(os.Args) < 2 {
		fatal("expected archive, checksums, or manifest")
	}
	var err error
	switch os.Args[1] {
	case "archive":
		err = archive(os.Args[2:])
	case "checksums":
		err = checksums(os.Args[2:])
	case "manifest":
		err = manifest(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fatal("%v", err)
	}
}

func archive(args []string) error {
	flags := flag.NewFlagSet("archive", flag.ContinueOnError)
	output := flags.String("output", "", "output tar.gz")
	mtimeRaw := flags.String("mtime", "", "Unix timestamp")
	var specs fileSpecs
	flags.Var(&specs, "file", "source=archive-name")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *output == "" || *mtimeRaw == "" || len(specs) == 0 {
		return errors.New("archive requires -output, -mtime, and at least one -file")
	}
	seconds, err := strconv.ParseInt(*mtimeRaw, 10, 64)
	if err != nil {
		return fmt.Errorf("parse mtime: %w", err)
	}
	entries := make([][2]string, 0, len(specs))
	for _, spec := range specs {
		parts := strings.SplitN(spec, "=", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" || filepath.IsAbs(parts[1]) || strings.Contains(parts[1], "..") {
			return fmt.Errorf("unsafe file specification %q", spec)
		}
		entries = append(entries, [2]string{parts[0], filepath.ToSlash(parts[1])})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i][1] < entries[j][1] })
	temporary, err := os.CreateTemp(filepath.Dir(*output), ".archive.*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	gz, err := gzip.NewWriterLevel(temporary, gzip.BestCompression)
	if err != nil {
		temporary.Close()
		return err
	}
	gz.Header.ModTime = time.Unix(seconds, 0).UTC()
	gz.Header.OS = 255
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		content, readErr := os.ReadFile(entry[0])
		if readErr != nil {
			return closeArchive(tw, gz, temporary, readErr)
		}
		mode := int64(0o644)
		if filepath.Base(entry[1]) == "hcorral" {
			mode = 0o755
		}
		header := &tar.Header{Name: entry[1], Mode: mode, Size: int64(len(content)), ModTime: time.Unix(seconds, 0).UTC(), AccessTime: time.Unix(seconds, 0).UTC(), ChangeTime: time.Unix(seconds, 0).UTC(), Uid: 0, Gid: 0, Uname: "root", Gname: "root", Format: tar.FormatPAX}
		if err := tw.WriteHeader(header); err != nil {
			return closeArchive(tw, gz, temporary, err)
		}
		if _, err := tw.Write(content); err != nil {
			return closeArchive(tw, gz, temporary, err)
		}
	}
	if err := closeArchive(tw, gz, temporary, nil); err != nil {
		return err
	}
	return os.Rename(temporaryPath, *output)
}

func closeArchive(tw *tar.Writer, gz *gzip.Writer, file *os.File, prior error) error {
	err1, err2, err3 := tw.Close(), gz.Close(), file.Close()
	if prior != nil {
		return prior
	}
	if err1 != nil {
		return err1
	}
	if err2 != nil {
		return err2
	}
	return err3
}

func checksums(args []string) error {
	flags := flag.NewFlagSet("checksums", flag.ContinueOnError)
	output := flags.String("output", "", "output checksum file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *output == "" || flags.NArg() == 0 {
		return errors.New("checksums requires -output and files")
	}
	files := append([]string(nil), flags.Args()...)
	sort.Strings(files)
	temporary, err := os.CreateTemp(filepath.Dir(*output), ".checksums.*")
	if err != nil {
		return err
	}
	path := temporary.Name()
	defer os.Remove(path)
	for _, name := range files {
		digest, _, err := hashFile(name)
		if err != nil {
			temporary.Close()
			return err
		}
		if _, err := fmt.Fprintf(temporary, "%s  %s\n", digest, filepath.Base(name)); err != nil {
			temporary.Close()
			return err
		}
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(path, *output)
}

func manifest(args []string) error {
	flags := flag.NewFlagSet("manifest", flag.ContinueOnError)
	output := flags.String("output", "", "output JSON")
	version := flags.String("version", "", "launcher version")
	commit := flags.String("commit", "", "source commit")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *output == "" || *version == "" || *commit == "" {
		return errors.New("manifest requires -output, -version, and -commit")
	}
	type artifact struct {
		Name   string `json:"name"`
		SHA256 string `json:"sha256"`
		Size   int64  `json:"size"`
	}
	items := []artifact{}
	files := append([]string(nil), flags.Args()...)
	sort.Strings(files)
	for _, name := range files {
		digest, size, err := hashFile(name)
		if err != nil {
			return err
		}
		items = append(items, artifact{filepath.Base(name), digest, size})
	}
	document := struct {
		Schema    int        `json:"schema"`
		Component string     `json:"component"`
		Version   string     `json:"version"`
		Commit    string     `json:"commit"`
		Artifacts []artifact `json:"artifacts"`
	}{1, "hcorral", *version, *commit, items}
	content, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	return os.WriteFile(*output, content, 0o600)
}

func hashFile(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	h := sha256.New()
	size, err := io.Copy(h, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), size, nil
}
func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "hcorral-pack: "+format+"\n", args...)
	os.Exit(2)
}
