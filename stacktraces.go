package deadlock

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

func callers(skip int) []uintptr {
	s := make([]uintptr, 50) // Most relevant context seem to appear near the top of the stack.
	return s[:runtime.Callers(2+skip, s)]
}

func printStack(w io.Writer, stack []uintptr) {
	home := os.Getenv("HOME")
	usr, err := user.Current()
	if err == nil {
		home = usr.HomeDir
	}
	cwd, _ := os.Getwd()

	frames := runtime.CallersFrames(stack)
	for {
		frame, more := frames.Next()

		name := frame.Function
		pkg := ""
		if pos := strings.LastIndex(name, "/"); pos >= 0 {
			name = name[pos+1:]
		}
		if pos := strings.Index(name, "."); pos >= 0 {
			pkg = name[:pos]
			name = name[pos+1:]
		}
		if pkg == "runtime" && name == "goexit" || pkg == "testing" && name == "tRunner" {
			fmt.Fprintln(w)
			return
		}

		// Shorten file path
		file := frame.File
		clean := file
		if cwd != "" {
			cl, err := filepath.Rel(cwd, file)
			if err == nil {
				clean = cl
			}
		}
		if home != "" {
			s2 := strings.Replace(file, home, "~", 1)
			if len(clean) > len(s2) {
				clean = s2
			}
		}

		tail := ""
		if !more {
			tail = " <<<<<" // Make the line performing a lock prominent.
		}
		fmt.Fprintf(w, "%s:%d %s.%s%s\n", clean, frame.Line, pkg, name, tail)

		if !more {
			break
		}
	}

	fmt.Fprintln(w)
}

var fileSources struct {
	sync.Mutex
	lines map[string][][]byte
}

// Reads souce file lines from disk if not cached already.
func getSourceLines(file string) [][]byte {
	fileSources.Lock()
	defer fileSources.Unlock()
	if fileSources.lines == nil {
		fileSources.lines = map[string][][]byte{}
	}
	if lines, ok := fileSources.lines[file]; ok {
		return lines
	}
	text, _ := ioutil.ReadFile(file)
	fileSources.lines[file] = bytes.Split(text, []byte{'\n'})
	return fileSources.lines[file]
}

func code(file string, line int) string {
	lines := getSourceLines(file)
	line -= 2
	if line >= len(lines) || line < 0 {
		return "???"
	}
	return "{ " + string(bytes.TrimSpace(lines[line])) + " }"
}

// Stacktraces for all goroutines.
func stacks() []byte {
	buf := make([]byte, 1024*16)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			return buf[:n]
		}
		buf = make([]byte, 2*len(buf))
	}
}
