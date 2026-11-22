package lang

import (
	"fmt"
	"path/filepath"
)

func biNav(in *Interp, io ioCtx, args []Value) error {
	start := in.Cwd()
	if len(args) > 0 {
		path, err := toArg(args[0])
		if err != nil {
			return err
		}
		start = filepath.Clean(in.abs(path))
	}
	if in.navigate == nil {
		return fmt.Errorf("nav needs a terminal")
	}
	dir, intr, err := in.navigate(start)
	if intr {
		return interrupted(Pos{})
	}
	if err != nil {
		return fmt.Errorf("nav: %w", err)
	}
	if dir == "" {
		return nil
	}
	if err := in.Cd(dir); err != nil {
		return fmt.Errorf("nav: %w", err)
	}
	return nil
}
