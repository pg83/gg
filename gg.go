package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const (
	ESC = "\x1b"
	RST = ESC + "[0m"
	R   = ESC + "[91m"
	G   = ESC + "[92m"
	Y   = ESC + "[93m"
	B   = ESC + "[94m"
	M   = ESC + "[95m"
)

func color(color string, s string) string {
	return color + s + RST
}

type Exception struct {
	what func() error
}

func (self *Exception) throw() {
	panic(self)
}

func (self *Exception) catch(cb func(*Exception)) {
	if self != nil {
		cb(self)
	}
}

func (self *Exception) fatal(code int, prefix string) {
	fmt.Fprintf(os.Stderr, "%s%s: %v%s\n", R, prefix, self.what(), RST)
	os.Exit(code)
}

func newException(e error) *Exception {
	return &Exception{
		what: func() error {
			return e
		},
	}
}

func fmtException(format string, args ...any) *Exception {
	return newException(fmt.Errorf(format, args...))
}

func try(cb func()) (err *Exception) {
	defer func() {
		if rec := recover(); rec != nil {
			if exc, ok := rec.(*Exception); ok {
				err = exc
			} else {
				// personality check failed
				panic(rec)
			}
		}
	}()

	cb()

	return nil
}

func lookPath(prog string) string {
	path, err := exec.LookPath(prog)

	if err != nil {
		newException(err).throw()
	}

	return path
}

type Tools map[string]string

func findTools() *Tools {
	return &Tools{
		"strip": lookPath("llvm-strip"),
		"objcopy": lookPath("llvm-objcopy"),
		"objdump": lookPath("llvm-objdump"),
		"ar": lookPath("llvm-ar"),
		"clang": lookPath("clang"),
		"clang++": lookPath("clang++"),
		"python3": lookPath("python3"),
	}
}

type Flags map[string]string

func commonFlags(tools *Tools) *Flags {
	res := Flags{
		"SANDBOXING": "yes",
		"APPLE_SDK_LOCAL": "yes",
		"CLANG_COVERAGE": "no",
		"CONSISTENT_DEBUG": "yes",
		"DISABLE_YMAKE_CONF_CUSTOMIZATION": "yes",
		"NO_DEBUGINFO": "yes",
		"OPENSOURCE": "yes",
		"OS_SDK": "local",
		"TIDY": "no",
		"USE_ARCADIA_PYTHON": "yes",
		"USE_CLANG_CL": "yes",
		"USE_PREBUILT_TOOLS": "no",
		"USE_PYTHON3": "yes",
		"BUILD_PYTHON_BIN": (*tools)["python3"],
		"BUILD_PYTHON3_BIN": (*tools)["python3"],
	}

	for k, v := range *tools {
		k = strings.ToUpper(strings.ReplaceAll(k, "+", "_pl"))

		res[k + "_TOOL"] = v
		res[k + "_TOOL_VENDOR"] = v
	}

	return &res
}

type RenderContext struct {
	Tools *Tools
	Flags *Flags
}

func newRenderContext() *RenderContext {
	tools := findTools()

	return &RenderContext{
		Tools: tools,
		Flags: commonFlags(tools),
	}
}

func run() {
    fmt.Println(newRenderContext())
}

func main() {
	try(func() {
		run()
	}).catch(func(exc *Exception) {
		exc.fatal(1, "abort")
	})
}
