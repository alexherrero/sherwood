package config

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInvalid is wrapped by every error that means a settings or strategy file
// cannot be used. Sherwood refuses to start on it.
var ErrInvalid = errors.New("config: invalid file")

// ValidationError lists every problem found in one file, so a single start-up
// attempt reports all of them rather than the first.
type ValidationError struct {
	// File names the kind of file: "settings file" or "strategy file".
	File string
	// Problems holds one plain sentence per problem, each naming the field.
	Problems []string
}

// Error joins the problems into one message.
func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s is invalid: %s", e.File, strings.Join(e.Problems, "; "))
}

// Is makes errors.Is(err, ErrInvalid) true for every ValidationError.
func (e *ValidationError) Is(target error) bool { return target == ErrInvalid }

// problems collects validation problems for one file.
type problems struct {
	file string
	list []string
}

func (p *problems) add(format string, args ...any) {
	p.list = append(p.list, fmt.Sprintf(format, args...))
}

func (p *problems) err() error {
	if len(p.list) == 0 {
		return nil
	}
	return &ValidationError{File: p.file, Problems: p.list}
}
