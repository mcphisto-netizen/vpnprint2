package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// logHandle escribe el registro al lado del programa (carpeta de datos)
// y lo rota cuando pasa de 5 MB.
type logHandle struct {
	mu   sync.Mutex
	path string
	f    *os.File
}

var glog logHandle

func initLog(dir string) *logHandle {
	glog.path = filepath.Join(dir, "registro.txt")
	glog.rotateIfNeeded()
	glog.append("=== inicio " + version + " (" + time.Now().Format("2006-01-02 15:04:05") + ") ===")
	return &glog
}

func (l *logHandle) maxBytes() int64 { return 5 * 1024 * 1024 }

func (l *logHandle) rotateIfNeeded() {
	st, err := os.Stat(l.path)
	if err == nil && st.Size() > l.maxBytes() {
		old := l.path[:len(l.path)-4] + ".viejo.txt"
		_ = os.Remove(old)
		_ = os.Rename(l.path, old)
	}
}

func (l *logHandle) append(line string) {
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().Format("2006-01-02 15:04:05"), line)
}

func (l *logHandle) add(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rotateIfNeeded()
	l.append("[" + fmt.Sprintf(format, args...) + "]")
}

func (l *logHandle) tail(n int) []string {
	b, err := os.ReadFile(l.path)
	if err != nil {
		return nil
	}
	lines := make([]string, 0, 16)
	for _, s := range stringSplitLines(b) {
		lines = append(lines, s)
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

func stringSplitLines(b []byte) []string {
	var out []string
	for _, s := range splitCRLF(string(b)) {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func splitCRLF(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}
