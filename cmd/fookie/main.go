package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage:")
		fmt.Fprintln(os.Stderr, "  fookie start|stop|status|logs [--profile full|minimal]")
		fmt.Fprintln(os.Stderr, "  fookie doctor")
		fmt.Fprintln(os.Stderr, "  fookie config")
		fmt.Fprintln(os.Stderr, "  fookie upgrade")
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
	case "doctor":
		printDoctor()
	case "config":
		printConfig()
	case "upgrade":
		fmt.Println("Upgrade flow is managed via release tags and image/chart updates.")
		fmt.Println("Use: git fetch --tags && checkout latest release tag.")
	case "start", "stop", "status", "logs":
		profile, rest := parseProfileArg(os.Args[2:])
		args := []string{"compose"}
		if profile == "minimal" {
			args = append(args, "-f", filepath.Join("demo", "docker-compose.minimal.yml"), "-f", filepath.Join("demo", "compose.demo.yml"))
		} else {
			args = append(args, "-f", filepath.Join("demo", "docker-compose.yml"), "-f", filepath.Join("demo", "compose.demo.yml"))
		}
		switch os.Args[1] {
		case "start":
			args = append(args, "up", "-d")
		case "stop":
			args = append(args, "down")
		case "status":
			args = append(args, "ps")
		case "logs":
			args = append(args, "logs", "-f")
		}
		args = append(args, rest...)
		run(root, "docker", args...)
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

func printDoctor() {
	cmds := []string{"docker", "go"}
	for _, c := range cmds {
		if _, err := exec.LookPath(c); err != nil {
			fmt.Printf("[missing] %s\n", c)
			continue
		}
		fmt.Printf("[ok] %s\n", c)
	}
}

func printConfig() {
	fmt.Printf("profile default: full\n")
	fmt.Printf("compose full: demo/docker-compose.yml + demo/compose.demo.yml\n")
	fmt.Printf("compose minimal: demo/docker-compose.minimal.yml + demo/compose.demo.yml\n")
}

func parseProfileArg(args []string) (string, []string) {
	profile := "full"
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--profile" && i+1 < len(args) {
			v := strings.ToLower(args[i+1])
			if v == "full" || v == "minimal" {
				profile = v
				i++
				continue
			}
		}
		if strings.HasPrefix(a, "--profile=") {
			v := strings.ToLower(strings.TrimPrefix(a, "--profile="))
			if v == "full" || v == "minimal" {
				profile = v
				continue
			}
		}
		out = append(out, a)
	}
	return profile, out
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
