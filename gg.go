package main

import (
	"fmt"
	"os"
	"sync"
	"maps"
	"bytes"
	"os/exec"
	"strings"
	"strconv"
	"io/ioutil"
	"path/filepath"
	"encoding/json"
	"encoding/base64"
	"github.com/jon-codes/getopt"
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

func throw(err error) {
	if err != nil {
		newException(err).throw()
	}
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

type Semaphore struct {
	ch chan struct{}
}

func newSemaphore(n int) *Semaphore {
	return &Semaphore{
		ch: make(chan struct{}, n),
	}
}

func (self *Semaphore) acquire() {
	self.ch <- struct{}{}
}

func (self *Semaphore) release() {
	<-self.ch
}

func lookPath(prog string) string {
	path, err := exec.LookPath(prog)

	throw(err)

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
		"ymake": lookPath("ymake"),
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
		"NEED_PLATFORM_PEERDIRS": "no",
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
	SrcRoot string
}

func findRoot() string {
	res, err := os.Getwd()

	throw(err)

	return res
}

func newRenderContext() *RenderContext {
	tools := findTools()
	root := findRoot()

	return &RenderContext{
		Tools: tools,
		Flags: commonFlags(tools),
		SrcRoot: root,
	}
}

type TCParams struct {
	C string `json:"c_compiler"`
	CXX string `json:"cxx_compiler"`
	ObjCopy string `json:"objcopy"`
	Strip string `json:"strip"`
	AR string `json:"ar"`
	Type string `json:"type"`
	Version string `json:"version"`
}

type TCPlatform struct {
	Arch string `json:"arch"`
	OS string `json:"os"`
	Toolchain string `json:"toolchain"`
}

type TCPlatforms struct {
	Host *TCPlatform `json:"host"`
	Target *TCPlatform `json:"target"`
}

type TCDescriptor struct {
	Flags *Flags `json:"flags"`
	Name string `json:"name"`
	Params *TCParams `json:"params"`
	Platform *TCPlatforms `json:"platform"`
	PlatformName string `json:"platform_name"`
	BuildType string `json:"build_type"`
}

func (self *TCDescriptor) encode() string {
	res, err := json.Marshal(*self)

	throw(err)

	return base64.StdEncoding.EncodeToString(res)
}

func (self *RenderContext) toolChainFor(extra Flags) *TCDescriptor {
	target := extra["GG_TARGET_PLATFORM"]
	fields := strings.Split(target, "-")

	if len(fields) != 3 {
		fmtException("wrong platform %s", target).throw()
	}

	plat := &TCPlatform{
		Arch: fields[2],
		OS: strings.ToUpper(fields[1]),
		Toolchain: fields[0],
	}

	flags := maps.Clone(*self.Flags)
	maps.Copy(flags, extra)

	return &TCDescriptor{
		Flags: &flags,
		Name: "clang",
		Params: &TCParams{
			C: (*self.Tools)["clang"],
			CXX: (*self.Tools)["clang++"],
			ObjCopy: (*self.Tools)["objcopy"],
			Strip: (*self.Tools)["strip"],
			AR: (*self.Tools)["ar"],
			Type: "clang",
			Version: "18",
		},
		Platform: &TCPlatforms{
			Host: plat,
			Target: plat,
		},
		PlatformName: strings.ToUpper(target),
		BuildType: flags["GG_BUILD_TYPE"],
	}
}

func (self *RenderContext) genConfFor(tc *TCDescriptor) []byte {
	args := []string{
		(*self.Tools)["python3"],
		self.SrcRoot + "/build/ymake_conf.py",
		self.SrcRoot,
		"dist-" + tc.BuildType,
		"no",
		"--toolchain-params",
		tc.encode(),
		"-l",
	}

	for k, v := range *tc.Flags {
		args = append(args, "-D")
		args = append(args, k + "=" + v)
	}

	env := append(os.Environ(), "PYTHONPATH=" + self.SrcRoot + "/contrib/python/six/py3")

	var outb bytes.Buffer
	var errb bytes.Buffer

	cmd := &exec.Cmd{
		Path: args[0],
		Args: args,
		Env: env,
		Dir: self.SrcRoot,
		Stdout: &outb,
		Stderr: &errb,
	}

	err := cmd.Run()

	if err != nil {
		fmtException("genconf failed:\n%s, %w", errb.String(), err).throw()
	}

	return outb.Bytes()
}

func (self *RenderContext) genGraphFor(conf []byte, targets []string, keepGoing bool) []byte {
	td, err := os.MkdirTemp("", "ymake")

	if err != nil {
		newException(err).throw()
	}

	defer os.RemoveAll(td)

	err = ioutil.WriteFile(td + "/conf", conf, 0666)

	throw(err)

	root := self.SrcRoot

	args := []string{
		(*self.Tools)["ymake"],
		"--warn", "dirloops,ChkPeers",
		"--write-meta-data", td + "/md",
		"--config", td + "/conf",
		"--plugins-root", root + "/build/plugins," + root + "/build/internal/plugins",
		"--build-root", td,
		"--source-root", root,
		"--makefiles-dart", td + "/dart",
		"--dump-build-plan", "-",
		"--events", "",
        }

	if keepGoing {
		args = append(args, "--keep-on")
	}

	args = append(args, targets...)

	var outb bytes.Buffer
	var errb bytes.Buffer

	cmd := &exec.Cmd{
		Path: args[0],
		Args: args,
		Dir: root,
		Stdout: &outb,
		Stderr: &errb,
	}

	err = cmd.Run()

	if err != nil {
		fmtException("%s%v", errb.String(), err).throw()
	}

	return outb.Bytes()
}

type Cmd struct {
	Args []string `json:"cmd_args"`
	Env map[string]string `json:"env"`
}

type Node struct {
	Uid string `json:"uid"`
	Cmds []Cmd `json:"cmds"`
	Inputs []string `json:"inputs"`
	Outputs []string `json:"outputs"`
	Deps []string `json:"deps"`
	KV map[string]string `json:"kv"`
	Env map[string]string `json:"env"`
}

type Proto struct {
	Graph []Node `json:"graph"`
	Result []string `json:"result"`
}

func parseGraph(data []byte) *Proto {
	var res Proto

	err := json.Unmarshal(data, &res)

	if err != nil {
		fmtException("can not parse ymake graph: %v", err).throw()
	}

	return &res
}

type Future struct {
	F func()
	O sync.Once
}

func (self *Future) callOnce() {
	self.O.Do(self.F)
}

type Executor struct {
	ByUid *map[string]*Future
	Sched *Semaphore
}

func (self *Executor) executeNode(node *Node) {
	self.Sched.acquire()

	defer self.Sched.release()

	for _, o := range node.Outputs {
		os.MkdirAll(filepath.Dir(o), os.ModePerm)
	}

	for _, c := range node.Cmds {
		res, err := exec.Command(c.Args[0], c.Args[1:]...).CombinedOutput()
		os.Stdout.Write(res)
		if err != nil {
			fmtException("%s: %w", c.Args, err).throw()
		}
	}
}

func checkExists(path string) bool {
	_, err := os.Stat(path)

	return err == nil
}

func complete(node *Node) bool {
	for _, o := range node.Outputs {
		if !checkExists(o) {
			return false
		}
	}

	return true
}

func (self *Executor) execute(node *Node) {
	if complete(node) {
		return
	}

	self.visitAll(node.Deps)
	self.executeNode(node)

	if !complete(node) {
		fmtException("node %s not complete", node).throw()
	}

	fmt.Printf("[%s] %s\n", color("OK", G), node.Outputs)
}

func newNodeFuture(ex *Executor, node *Node) *Future {
	return &Future{F: func() {
		ex.execute(node)
	}}
}

func newExecutor(nodes []Node, threads int) *Executor {
	deps := map[string]*Future{}

	res := &Executor{
		ByUid: &deps,
		Sched: newSemaphore(threads),
	}

	for _, n := range nodes {
		deps[n.Uid] = newNodeFuture(res, &n)
	}

	return res
}

func (self *Executor) visitAll(uids []string) {
	if len(uids) == 0 {
		return
	}

	if len(uids) == 1 {
		(*self.ByUid)[uids[0]].callOnce()

		return
	}

	wg := &sync.WaitGroup{}

	for _, u := range uids {
		f := (*self.ByUid)[u]

		wg.Add(1)

		go func() {
			defer wg.Done()

			try(func() {
				f.callOnce()
			}).catch(func(exc *Exception) {
				exc.fatal(2, "subcommand error")
			})
		}()
	}

	wg.Wait()
}

func handleMake(args []string) {
	rc := newRenderContext()

	flags := Flags{
		"MUSL": "yes",
		"USE_ICONV": "static",
		"GG_BUILD_TYPE": "release",
		"GG_TARGET_PLATFORM": "default-linux-x86_64",
		"USER_CFLAGS": os.Getenv("CFLAGS"),
		"USER_CONLYFLAGS": os.Getenv("CONLYFLAGS"),
		"USER_CXXFLAGS": os.Getenv("CXXFLAGS"),
		"USER_LDFLAGS": os.Getenv("LDFLAGS") + " -fuse-ld=lld",
	}

	state := getopt.NewState(args)

	config := getopt.Config{
		Opts: getopt.OptStr(`kD:j:`),
		Mode: getopt.ModeInOrder,
	}

	targets := []string{}
	keep := false
	threads := 1

	for opt, err := range state.All(config) {
		if err != nil {
			if err == getopt.ErrDone {
				break
			}
			throw(err)
		}

		if opt.Char == 'k' {
			keep = true
		} else if opt.Char == 'D' {
			fields := strings.Split(opt.OptArg, "-")

			if len(fields) != 2 {
				fmtException("malformed flag %s", opt.OptArg).throw()
			}

			flags[fields[0]] = fields[1]
		} else if opt.Char == 'j' {
			i, err := strconv.Atoi(opt.OptArg)
			throw(err)
			threads = i
		} else if opt.Char == 1 {
			targets = append(targets, opt.OptArg)
		} else {
			fmtException("unhandled flag %s", opt.Char).throw()
		}
	}

	tc := rc.toolChainFor(flags)
	conf := rc.genConfFor(tc)
	graph := string(rc.genGraphFor(conf, targets, keep))

	graph = strings.ReplaceAll(graph, "$(BUILD_ROOT)", rc.SrcRoot)
	graph = strings.ReplaceAll(graph, "$(SOURCE_ROOT)", rc.SrcRoot)

	proto := parseGraph([]byte(graph))

	newExecutor(proto.Graph, threads).visitAll(proto.Result)
}

func help() {
	fmt.Println("available handlers:")
	fmt.Println("    make")
	fmt.Println("run gg [handler] --help for extra help")
}

func run(args []string) {
	if len(args) == 0 {
		help()
	} else if args[0] == "make" {
		handleMake(args)
	} else {
		help()
	}
}

func main() {
	try(func() {
		run(os.Args[1:])
	}).catch(func(exc *Exception) {
		exc.fatal(1, "abort")
	})
}
