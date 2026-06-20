package mekami

import "os"

// devNullReader returns an *os.File open on os.DevNull in
// read-only mode. Used as cmd.Stdin to detach a child process
// from the parent's tty / console. The file is safe to leave open
// until the child closes its own end of the handle.
func devNullReader() *os.File {
	f, err := os.Open(os.DevNull)
	if err != nil {
		// os.DevNull must always open. A failure here is a
		// programmer error (wrong platform constant) or an
		// exhausted-fd situation, both of which will
		// surface immediately when cmd.Start() runs.
		return nil
	}
	return f
}

// devNullWriter returns an *os.File open on os.DevNull in
// write-only mode. Used as cmd.Stdout and cmd.Stderr for detached
// children. See devNullReader for the error semantics.
func devNullWriter() *os.File {
	f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return nil
	}
	return f
}
