//go:build windows

package remux

import "os/exec"

func configureAudioProcess(*exec.Cmd) {}

func terminateAudioProcess(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
