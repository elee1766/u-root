// Copyright 2012-2017 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// grep searches file contents using regular expressions.
//
// Synopsis:
//
//	grep [-vrlq] [FILE]...
//
// Options:
//
//	-v: print only non-matching lines
//	-r: recursive
//	-l: list only files
//	-q: don't print matches; exit on first match
package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	flag "github.com/spf13/pflag"
)

var errQuite = fmt.Errorf("not found")

type grepResult struct {
	c       *grepCommand
	line    *string
	lineNum int
	match   bool
}

type grepCommand struct {
	rc   io.ReadCloser
	name string
}

var (
	expr            = flag.StringP("regexp", "e", "", "Pattern to match")
	headers         = flag.BoolP("no-filename", "h", false, "Suppress file name prefixes on output")
	invert          = flag.BoolP("invert-match", "v", false, "Print only non-matching lines")
	recursive       = flag.BoolP("recursive", "r", false, "recursive")
	noShowMatch     = flag.BoolP("files-with-matches", "l", false, "list only files")
	count           = flag.BoolP("count", "c", false, "Just show counts")
	caseInsensitive = flag.BoolP("ignore-case", "i", false, "case-insensitive matching")
	number          = flag.BoolP("line-number", "n", false, "Show line numbers")
	fixed           = flag.BoolP("fixed-strings", "F", false, "Match using fixed strings")
)

// grep reads data from the os.File embedded in grepCommand.
// It creates a chan of grepResults and pushes a pointer to it into allGrep.
// It matches each line against the re and pushes the matching result
// into the chan.
// If we are only looking for a match, we exit as soon as the condition is met.
// "match" means result of re.Match == match flag.
func (c *cmd) grep(f *grepCommand, re *regexp.Regexp) (ok bool) {
	r := bufio.NewReader(f.rc)
	defer f.rc.Close()
	var lineNum int
	for {
		i, err := r.ReadString('\n')
		if err != nil {
			break
		}
		var m bool
		switch {
		case c.fixed && c.caseInsensitive:
			m = strings.Contains(strings.ToLower(i), strings.ToLower(c.expr))
		case c.fixed && !c.caseInsensitive:
			m = strings.Contains(i, c.expr)
		default:
			m = re.Match([]byte(i))
		}
		if m != c.invert {
			// in quiet mode, exit before the first match
			if c.quiet {
				return false
			}
			c.printMatch(f, &i, lineNum+1, m)
			if c.noShowMatch {
				break
			}
		}
		lineNum++
	}
	return true
}

func (c *cmd) printMatch(
	cmd *grepCommand,
	line *string,
	lineNum int,
	match bool,
) {
	var prefix string
	if match == !c.invert {
		c.matchCount++
	}
	if c.count {
		return
	}
	if c.showName {
		fmt.Fprintf(c.stdout, "%v", cmd.name)
		prefix = ":"
	}
	if c.noShowMatch {
		fmt.Fprintf(c.stdout, "\n")
		return
	}
	if c.number {
		prefix = fmt.Sprintf("%d:", lineNum)
	}
	if match == !c.invert {
		fmt.Fprintf(c.stdout, "%v%v", prefix, *line)
	}
}

type params struct {
	expr            string
	headers         bool
	invert          bool
	recursive       bool
	noShowMatch     bool
	count           bool
	caseInsensitive bool
	number          bool
	quiet           bool
	fixed           bool
}

type cmd struct {
	stdin  io.ReadCloser
	stdout io.Writer
	stderr io.Writer
	args   []string
	params
	matchCount int
	showName   bool
}

func command(stdin io.ReadCloser, stdout io.Writer, stderr io.Writer, p params, args []string) *cmd {
	return &cmd{
		stdin:  stdin,
		stdout: stdout,
		stderr: stderr,
		params: p,
		args:   args,
	}
}

func main() {
	flag.Parse()
	p := params{
		expr:            *expr,
		headers:         *headers,
		invert:          *invert,
		recursive:       *recursive,
		noShowMatch:     *noShowMatch,
		count:           *count,
		caseInsensitive: *caseInsensitive,
		number:          *number,
		quiet:           *quiet,
		fixed:           *fixed,
	}

	if err := command(os.Stdin, os.Stdout, os.Stderr, p, flag.Args()).run(); err != nil {
		if err == errQuite {
			os.Exit(1)
		}
		log.Fatal(err)
	}
}
func (c *cmd) run() error {
	// parse the expression into valid regex
	if c.expr != "" {
		c.args = append([]string{c.expr}, c.args...)
	}
	r := ".*"
	if len(c.args) > 0 {
		r = c.args[0]
	}
	if c.caseInsensitive && !strings.HasPrefix(r, "(?i)") && !c.fixed {
		r = "(?i)" + r
	}
	var re *regexp.Regexp
	if !c.fixed {
		re = regexp.MustCompile(r)
	} else if c.expr == "" {
		c.expr = c.args[0]
	}

	// start producing greps in a goroutine
	// very special case, just stdin
	if len(c.args) < 2 {
		if !c.grep(&grepCommand{c.stdin, "<stdin>"}, re) {
			return nil
		}
	} else {
		c.showName = (len(c.args[1:]) > 1 || c.recursive || c.noShowMatch) && !c.headers
		for _, v := range c.args[1:] {
			err := filepath.Walk(v, func(name string, fi os.FileInfo, err error) error {
				if err != nil {
					fmt.Fprintf(c.stderr, "grep: %v: %v\n", name, err)
					return nil
				}
				if fi.IsDir() && !c.recursive {
					fmt.Fprintf(c.stderr, "grep: %v: Is a directory\n", name)
					return filepath.SkipDir
				}
				fp, err := os.Open(name)
				if err != nil {
					fmt.Fprintf(c.stderr, "can't open %s: %v\n", name, err)
					return nil
				}
				if !c.grep(&grepCommand{fp, name}, re) {
					return errQuite
				}
				return nil
			})
			// reuse the errQuite as a value that lets us know if we should not return an errQuite
			if errors.Is(err, errQuite) {
				return nil
			}
			if err != nil {
				return err
			}
		}
	}
	if c.quiet {
		return errQuite
	}
	if c.count {
		fmt.Fprintf(c.stdout, "%d\n", c.matchCount)
	}
	return nil
}
