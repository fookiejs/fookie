package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage:")
		fmt.Fprintln(os.Stderr, "  fookie compose <args>   — docker compose -f demo/docker-compose.yml -f demo/compose.demo.yml <args>")
		fmt.Fprintln(os.Stderr, "  fookie platform <args> — docker compose -f deploy/compose/postgres.yml -f deploy/compose/observability.yml -f deploy/compose/apps.yml <args>")
		fmt.Fprintln(os.Stderr, "  fookie helm <args>      — helm in repo root (e.g. fookie helm template fookie ./charts/fookie)")
		os.Exit(2)
	}
	root, err := findRepoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	switch os.Args[1] {
	case "compose":
		args := []string{"compose",
			"-f", filepath.Join("demo", "docker-compose.yml"),
			"-f", filepath.Join("demo", "compose.demo.yml"),
		}
		if len(os.Args) > 2 {
			args = append(args, os.Args[2:]...)
		}
		run(root, "docker", args...)
	case "platform":
		args := []string{"compose",
			"-f", filepath.Join("deploy", "compose", "postgres.yml"),
			"-f", filepath.Join("deploy", "compose", "observability.yml"),
			"-f", filepath.Join("deploy", "compose", "apps.yml"),
		}
		if len(os.Args) > 2 {
			args = append(args, os.Args[2:]...)
		}
		run(root, "docker", args...)
	case "helm":
		hargs := os.Args[2:]
		if len(hargs) == 0 {
			hargs = []string{"template", "fookie", filepath.Join("charts", "fookie")}
		}
		run(root, "helm", hargs...)
	default:
		fmt.Fprintln(os.Stderr, "unknown command:", os.Args[1])
		os.Exit(2)
	}
}

func findRepoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found from %s", wd)
		}
		dir = parent
	}
}

func run(dir, name string, arg ...string) {
	cmd := exec.Command(name, arg...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		os.Exit(1)
	}
}
