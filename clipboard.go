package main

import (
	"bytes"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"unicode/utf16"
)

func copyToClipboard(text string) error {
	switch runtime.GOOS {
	case "windows":
		text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\n", "\r\n")
		u := utf16.Encode([]rune(text))
		buf := bytes.NewBuffer([]byte{0xFF, 0xFE}) // UTF-16LE BOM, understood by clip.exe
		for _, c := range u {
			buf.WriteByte(byte(c))
			buf.WriteByte(byte(c >> 8))
		}
		cmd := exec.Command("clip")
		cmd.Stdin = buf
		return cmd.Run()
	case "darwin":
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(text)
		return cmd.Run()
	}
	for _, c := range [][]string{{"wl-copy"}, {"xclip", "-selection", "clipboard"}, {"xsel", "-b", "-i"}} {
		if p, err := exec.LookPath(c[0]); err == nil {
			cmd := exec.Command(p, c[1:]...)
			cmd.Stdin = strings.NewReader(text)
			if cmd.Run() == nil {
				return nil
			}
		}
	}
	return errors.New("no clipboard tool found (install xclip / wl-clipboard)")
}
