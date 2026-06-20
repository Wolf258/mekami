package mekami

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSeekBackNLines checks that the helper positions the file
// pointer at the start of the n-th newline from the end, and
// falls back to offset 0 when the file is smaller than n lines.
func TestSeekBackNLines(t *testing.T) {
	cases := []struct {
		name    string
		content string
		n       int
		want    string // expected contents from the seek point to EOF
	}{
		{
			name:    "empty file",
			content: "",
			n:       5,
			want:    "",
		},
		{
			name:    "fewer lines than n",
			content: "a\nb\n",
			n:       10,
			want:    "a\nb\n",
		},
		{
			name:    "exactly n lines",
			content: "1\n2\n3\n4\n5\n",
			n:       5,
			want:    "1\n2\n3\n4\n5\n",
		},
		{
			name:    "more than n lines keeps last n",
			content: "1\n2\n3\n4\n5\n6\n7\n",
			n:       3,
			want:    "5\n6\n7\n",
		},
		{
			name:    "no trailing newline",
			content: "1\n2\n3\n4\n5",
			n:       2,
			want:    "4\n5",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "log")
			if err := os.WriteFile(p, []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			f, err := os.Open(p)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err := seekBackNLines(f, tc.n); err != nil {
				t.Fatal(err)
			}
			buf, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			// Read from current position to EOF.
			rest, err := readAllFromCurrent(f)
			if err != nil {
				t.Fatal(err)
			}
			_ = buf
			if string(rest) != tc.want {
				t.Fatalf("got %q, want %q", string(rest), tc.want)
			}
		})
	}
}

// readAllFromCurrent reads the file from its current position to
// EOF into a string. It is a tiny helper used only by the test
// suite; using io.ReadAll would require juggling offsets.
func readAllFromCurrent(f *os.File) (string, error) {
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	cur, err := f.Seek(0, 1) // current position
	if err != nil {
		return "", err
	}
	size := info.Size()
	if cur >= size {
		return "", nil
	}
	buf := make([]byte, size-cur)
	if _, err := f.Read(buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

// TestFollowFile_SeesNewContent drives followFileContext with a
// cancellable context, writes to the file after followFile
// starts, and verifies the new bytes appear on stdout.
func TestFollowFile_SeesNewContent(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test.log")
	// Seed with a few lines.
	seed := "line1\nline2\nline3\n"
	if err := os.WriteFile(logPath, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	// Capture stdout.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	origStdout := os.Stdout
	os.Stdout = w
	t.Cleanup(func() {
		os.Stdout = origStdout
	})
	// Read captured output in the background.
	done := make(chan string, 1)
	go func() {
		var sb strings.Builder
		buf := make([]byte, 1024)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				sb.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
		done <- sb.String()
	}()
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- followFileContext(ctx, logPath)
	}()
	// Give the poller a moment to drain the seed.
	time.Sleep(300 * time.Millisecond)
	// Append a new line.
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("line4\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	// Wait for the poller to pick it up.
	time.Sleep(400 * time.Millisecond)
	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("followFile: %v", err)
	}
	_ = w.Close()
	out := <-done
	if !strings.Contains(out, "line1") {
		t.Errorf("expected seed line1 in output, got %q", out)
	}
	if !strings.Contains(out, "line4") {
		t.Errorf("expected appended line4 in output, got %q", out)
	}
}
