package main

import (
	"debug/elf"
	"debug/macho"
	"errors"
	"flag"
	"fmt"
	"path"
	"strings"
)

// Verify the actual executable before packaging it. CGO_ENABLED=0 is an input,
// not evidence that a later dependency or build change kept the artifact static.
func linkage(args []string) error {
	flags := flag.NewFlagSet("linkage", flag.ContinueOnError)
	platform := flags.String("os", "", "target OS")
	arch := flags.String("arch", "", "target architecture")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 || (*arch != "amd64" && *arch != "arm64") {
		return errors.New("linkage requires -os linux|darwin -arch amd64|arm64 executable")
	}
	switch *platform {
	case "linux":
		file, err := elf.Open(flags.Arg(0))
		if err != nil {
			return err
		}
		defer file.Close()
		want := elf.EM_X86_64
		if *arch == "arm64" {
			want = elf.EM_AARCH64
		}
		if file.Machine != want || file.Class != elf.ELFCLASS64 || file.Type != elf.ET_EXEC {
			return fmt.Errorf("unexpected Linux executable header: %s/%s/%s", file.Machine, file.Class, file.Type)
		}
		for _, program := range file.Progs {
			if program.Type == elf.PT_INTERP || program.Type == elf.PT_DYNAMIC {
				return fmt.Errorf("Linux binary is not fully static: %s present", program.Type)
			}
		}
	case "darwin":
		file, err := macho.Open(flags.Arg(0))
		if err != nil {
			return err
		}
		defer file.Close()
		want := macho.CpuAmd64
		if *arch == "arm64" {
			want = macho.CpuArm64
		}
		if file.Cpu != want || file.Type != macho.TypeExec {
			return fmt.Errorf("unexpected Darwin executable header: %s/%s", file.Cpu, file.Type)
		}
		libraries, err := file.ImportedLibraries()
		if err != nil {
			return err
		}
		for _, library := range libraries {
			library = path.Clean(library)
			if !strings.HasPrefix(library, "/usr/lib/") && !strings.HasPrefix(library, "/System/Library/Frameworks/") {
				return fmt.Errorf("Darwin binary requires a non-system library: %s", library)
			}
		}
	default:
		return fmt.Errorf("unsupported executable target %q", *platform)
	}
	return nil
}
