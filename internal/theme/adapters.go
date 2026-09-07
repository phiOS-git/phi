package theme

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Class is a themed target's reload class (design/adapters.txt, master plan
// §6.7): A reloads hot, B reconfigures at runtime, C needs a restart that
// Set never performs itself.
type Class string

const (
	ClassA Class = "A"
	ClassB Class = "B"
	ClassC Class = "C"
)

// Adapter is one row of design/adapters.txt.
type Adapter struct {
	Template    string // path from the repository root
	Destination string // path relative to $HOME
	Reload      string // a shell command, "-" (no reload path), or "[unknown]" (not wired yet)
	Class       Class
}

// ParseAdapters reads design/adapters.txt: template | destination | reload | class.
func ParseAdapters(root string) ([]Adapter, error) {
	path := filepath.Join(root, "design", "adapters.txt")
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("design/adapters.txt is missing: %w", err)
	}
	defer f.Close()

	var out []Adapter
	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "|")
		if len(fields) != 4 {
			return nil, fmt.Errorf("design/adapters.txt:%d: expected 4 fields separated by '|', got %d", lineNo, len(fields))
		}
		class := Class(strings.TrimSpace(fields[3]))
		if class != ClassA && class != ClassB && class != ClassC {
			return nil, fmt.Errorf("design/adapters.txt:%d: unknown class %q", lineNo, class)
		}
		out = append(out, Adapter{
			Template:    strings.TrimSpace(fields[0]),
			Destination: strings.TrimSpace(fields[1]),
			Reload:      strings.TrimSpace(fields[2]),
			Class:       class,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
