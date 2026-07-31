package main

import (
	"fmt"
	"strings"

	"github.com/rajifafif/lamigrate"
)

const defaultSeedDirectory = "sql/seeders"

// extractSeedDir removes the seed-only global flag before the generic global
// flag parser runs. Seed directory is deliberately separate from -dir, which
// continues to identify migration files.
func extractSeedDir(args []string) (string, []string, error) {
	seedDir := defaultSeedDirectory
	remaining := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-seed-dir" || arg == "--seed-dir":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return "", nil, fmt.Errorf("global flag %s requires a value", arg)
			}
			i++
			seedDir = args[i]
		case strings.HasPrefix(arg, "-seed-dir="):
			seedDir = strings.TrimPrefix(arg, "-seed-dir=")
		case strings.HasPrefix(arg, "--seed-dir="):
			seedDir = strings.TrimPrefix(arg, "--seed-dir=")
		default:
			remaining = append(remaining, arg)
		}
	}
	if strings.TrimSpace(seedDir) == "" {
		return "", nil, fmt.Errorf("global flag -seed-dir requires a non-empty value")
	}
	return seedDir, remaining, nil
}

// parseSeedRequest accepts Laravel-style --class SeederName after seed/db:seed.
func parseSeedRequest(command string, args []string, seedDir string) (lamigrate.SeedRequest, error) {
	if command != "seed" && command != "db:seed" {
		return lamigrate.SeedRequest{}, nil
	}

	request := lamigrate.SeedRequest{Directory: seedDir}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--class":
			if request.Class != "" {
				return lamigrate.SeedRequest{}, fmt.Errorf("seed: --class may only be provided once")
			}
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return lamigrate.SeedRequest{}, fmt.Errorf("seed: --class requires a value")
			}
			i++
			request.Class = args[i]
		case strings.HasPrefix(arg, "--class="):
			if request.Class != "" {
				return lamigrate.SeedRequest{}, fmt.Errorf("seed: --class may only be provided once")
			}
			request.Class = strings.TrimPrefix(arg, "--class=")
		default:
			return lamigrate.SeedRequest{}, fmt.Errorf("seed: unexpected argument %q; use --class <SeederName>", arg)
		}
	}
	return request, nil
}
