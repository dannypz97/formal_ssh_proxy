// Command cleanlog prints raw ssh-proxy session logs as readable text, using
// the same cleaning as the LLM summarizer.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/dparv/ssh-proxy/internal/transcript"
)

const usage = `Usage: cleanlog [-w] [file ...]

Prints raw ssh-proxy session logs as readable text: one command per line,
backspaces applied, escape sequences (arrow keys, cursor reports) removed and
control keys shown as e.g. [Ctrl+C].

Wildcards such as logs/*.log are expanded by cleanlog itself, so they work in
PowerShell and 'docker compose exec' too. With no file, or "-", reads stdin.

Flags:
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cleanlog", flag.ContinueOnError)
	fs.SetOutput(stderr)
	write := fs.Bool("w", false, "write <name>.txt next to each log instead of printing")
	fs.Usage = func() {
		fmt.Fprint(stderr, usage)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	files, err := expand(fs.Args())
	if err != nil {
		fmt.Fprintln(stderr, "cleanlog:", err)
		return 1
	}
	if len(files) == 0 {
		files = []string{"-"}
	}

	status := 0
	for i, name := range files {
		if err := cleanOne(name, *write, len(files) > 1, i > 0, stdin, stdout, stderr); err != nil {
			fmt.Fprintln(stderr, "cleanlog:", err)
			status = 1
		}
	}
	return status
}

// expand resolves wildcard patterns. Arguments without wildcards are kept as
// is, so a missing file is reported when it is opened.
func expand(args []string) ([]string, error) {
	var files []string
	for _, a := range args {
		if a == "-" || !strings.ContainsAny(a, "*?[") {
			files = append(files, a)
			continue
		}
		matches, err := filepath.Glob(a)
		if err != nil {
			return nil, fmt.Errorf("bad pattern %q: %w", a, err)
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("no files match %q", a)
		}
		files = append(files, matches...)
	}
	return files, nil
}

func cleanOne(name string, write, header, gap bool, stdin io.Reader, stdout, stderr io.Writer) error {
	var raw []byte
	var err error
	if name == "-" {
		if write {
			return fmt.Errorf("-w needs file names, not stdin")
		}
		raw, err = io.ReadAll(stdin)
	} else {
		raw, err = os.ReadFile(name)
	}
	if err != nil {
		return err
	}

	text := transcript.Clean(string(raw))
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}

	if write {
		out := strings.TrimSuffix(name, ".log") + ".txt"
		if err := os.WriteFile(out, []byte(text), 0o640); err != nil {
			return err
		}
		fmt.Fprintln(stderr, "wrote", out)
		return nil
	}

	if header {
		if gap {
			fmt.Fprintln(stdout)
		}
		fmt.Fprintf(stdout, "==> %s <==\n", name)
	}
	_, err = io.WriteString(stdout, text)
	return err
}
