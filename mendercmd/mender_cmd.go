package mendercmd

import (
	"bufio"
	"fmt"
	"os/exec"
)

// Commander interface represents a generic tool interface
type Commander interface {
	Commit() error
	Install(url string, done chan error, progress chan string) error
	Rollback() error
}

// MenderCommand serves as the implementation of the commander interface
type MenderCommand struct {
}

func execMender(done chan error, progress chan string, args ...string) error {
	cmd := exec.Command("mender", args...)
	stdout, _ := cmd.StdoutPipe()
	cmd.Start()
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		m := scanner.Text()
		if progress != nil {
			progress <- m
		}
		fmt.Println(m)
	}
	err := cmd.Wait()
	if done != nil {
		done <- err
	}
	return err
}

// Install runs the mender install
func (m *MenderCommand) Install(url string, done chan error, progress chan string) error {
	return execMender(done, progress, "-install", url)
}

// Commit runs mender commit
func (m *MenderCommand) Commit() error {
	return execMender(nil, nil, "-commit")
}

// Rollback runs mender rollback
func (m *MenderCommand) Rollback() error {
	return execMender(nil, nil, "-rollback")
}
