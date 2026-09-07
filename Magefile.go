//go:build mage

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const serverPackage = "./cmd/server"

// Default builds the production server.
var Default = Build

// Build creates the production server binary in bin/server.
func Build() error {
	if err := os.MkdirAll("bin", 0o755); err != nil {
		return fmt.Errorf("create bin directory: %w", err)
	}
	output := filepath.Join("bin", "server")
	return run("go", "build", "-trimpath", "-ldflags=-s -w", "-o", output, serverPackage)
}

// Test runs the complete Go test suite.
func Test() error {
	return run("go", "test", "./...")
}

// Verify runs tests and then produces the Render binary.
func Verify() error {
	if err := Test(); err != nil {
		return err
	}
	return Build()
}

// Clean removes Mage build output.
func Clean() error {
	if err := os.RemoveAll("bin"); err != nil {
		return fmt.Errorf("remove bin directory: %w", err)
	}
	return nil
}

func run(name string, args ...string) error {
	command := exec.Command(name, args...)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.Env = os.Environ()
	if err := command.Run(); err != nil {
		return fmt.Errorf("%s %v: %w", name, args, err)
	}
	return nil
}
