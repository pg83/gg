package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/jon-codes/getopt"
	"io/ioutil"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

const (
	ESC = "\x1b"
	RST = ESC + "[0m"
)

var COLS = map[string]string{
	"red":           ESC + "[31m",
	"green":         ESC + "[32m",
	"yellow":        ESC + "[33m",
	"blue":          ESC + "[34m",
	"magenta":       ESC + "[35m",
	"cyan":          ESC + "[36m",
	"white":         ESC + "[37m",
	"light-red":     ESC + "[91m",
	"light-green":   ESC + "[92m",
	"light-yellow":  ESC + "[93m",
	"light-blue":    ESC + "[94m",
	"light-magenta": ESC + "[95m",
	"light-cyan":    ESC + "[96m",
	"light-white":   ESC + "[97m",
}

func color(color string, s string) string {
	return COLS[color] + s + RST
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
	msg := fmt.Sprintf("%s: %v", prefix, self.what())
	fmt.Fprintf(os.Stderr, "%s\n", color("red", msg))
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
		"strip":   lookPath("llvm-strip"),
		"objcopy": lookPath("llvm-objcopy"),
		"objdump": lookPath("llvm-objdump"),
		"ar":      lookPath("llvm-ar"),
		"clang":   lookPath("clang"),
		"clang++": lookPath("clang++"),
		"python3": lookPath("python3"),
		"ymake":   lookPath("ymake"),
	}
}

type Flags map[string]string

func commonFlags(tools *Tools) *Flags {
	res := Flags{
		"APPLE_SDK_LOCAL":                  "yes",
		"CLANG_COVERAGE":                   "no",
		"CONSISTENT_DEBUG":                 "yes",
		"DISABLE_YMAKE_CONF_CUSTOMIZATION": "yes",
		"NO_DEBUGINFO":                     "yes",
		"OPENSOURCE":                       "yes",
		"OS_SDK":                           "local",
		"TIDY":                             "no",
		"USE_ARCADIA_PYTHON":               "yes",
		"USE_CLANG_CL":                     "yes",
		"USE_PREBUILT_TOOLS":               "no",
		"USE_PYTHON3":                      "yes",
		"BUILD_PYTHON_BIN":                 (*tools)["python3"],
		"BUILD_PYTHON3_BIN":                (*tools)["python3"],
	}

	for k, v := range *tools {
		k = strings.ToUpper(strings.ReplaceAll(k, "+", "_pl"))

		res[k+"_TOOL"] = v
		res[k+"_TOOL_VENDOR"] = v
	}

	return &res
}

type RenderContext struct {
	Tools   *Tools
	Flags   *Flags
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
		Tools:   tools,
		Flags:   commonFlags(tools),
		SrcRoot: root,
	}
}

type TCParams struct {
	C       string `json:"c_compiler"`
	CXX     string `json:"cxx_compiler"`
	ObjCopy string `json:"objcopy"`
	Strip   string `json:"strip"`
	AR      string `json:"ar"`
	Type    string `json:"type"`
	Version string `json:"version"`
}

type TCPlatform struct {
	Arch      string `json:"arch"`
	OS        string `json:"os"`
	Toolchain string `json:"toolchain"`
}

type TCPlatforms struct {
	Host   *TCPlatform `json:"host"`
	Target *TCPlatform `json:"target"`
}

type TCDescriptor struct {
	Flags        *Flags       `json:"flags"`
	Name         string       `json:"name"`
	Params       *TCParams    `json:"params"`
	Platform     *TCPlatforms `json:"platform"`
	PlatformName string       `json:"platform_name"`
	BuildType    string       `json:"build_type"`
}

func (self *TCDescriptor) encode() string {
	return base64.StdEncoding.EncodeToString(dumps(self))
}

func (self *RenderContext) toolChainFor(extra Flags) *TCDescriptor {
	target := extra["GG_TARGET_PLATFORM"]
	fields := strings.Split(target, "-")

	if len(fields) != 3 {
		fmtException("wrong platform %s", target).throw()
	}

	plat := &TCPlatform{
		Arch:      fields[2],
		OS:        strings.ToUpper(fields[1]),
		Toolchain: fields[0],
	}

	flags := maps.Clone(*self.Flags)
	maps.Copy(flags, extra)

	return &TCDescriptor{
		Flags: &flags,
		Name:  "clang",
		Params: &TCParams{
			C:       (*self.Tools)["clang"],
			CXX:     (*self.Tools)["clang++"],
			ObjCopy: (*self.Tools)["objcopy"],
			Strip:   (*self.Tools)["strip"],
			AR:      (*self.Tools)["ar"],
			Type:    "clang",
			Version: "18",
		},
		Platform: &TCPlatforms{
			Host:   plat,
			Target: plat,
		},
		PlatformName: strings.ToUpper(target),
		BuildType:    flags["GG_BUILD_TYPE"],
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
		args = append(args, k+"="+v)
	}

	env := append(os.Environ(), "PYTHONPATH="+self.SrcRoot+"/contrib/python/six/py3")

	var outb bytes.Buffer
	var errb bytes.Buffer

	cmd := &exec.Cmd{
		Path:   args[0],
		Args:   args,
		Env:    env,
		Dir:    self.SrcRoot,
		Stdout: &outb,
		Stderr: &errb,
	}

	err := cmd.Run()

	if err != nil {
		fmtException("genconf failed:\n%s, %v", errb.String(), err).throw()
	}

	return outb.Bytes()
}

func (self *RenderContext) genGraphFor(conf []byte, targets []string, keepGoing bool) []byte {
	td, err := os.MkdirTemp("", "ymake")

	throw(err)

	defer os.RemoveAll(td)

	throw(ioutil.WriteFile(td+"/conf", conf, 0666))

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
		Path:   args[0],
		Args:   args,
		Dir:    root,
		Stdout: &outb,
		Stderr: &errb,
	}

	err = cmd.Run()

	if err != nil {
		fmtException("%s%v", errb.String(), err).throw()
	}

	return outb.Bytes()
}

func (self *RenderContext) mountNode(node *Node) *Node {
	data := dumps(node)

	data = bytes.ReplaceAll(data, []byte("$(BUILD_ROOT)"), []byte(self.SrcRoot))
	data = bytes.ReplaceAll(data, []byte("$(SOURCE_ROOT)"), []byte(self.SrcRoot))

	return loads[Node](data)
}

type Cmd struct {
	Args   []string          `json:"cmd_args"`
	Env    map[string]string `json:"env"`
	StdOut *string           `json:"stdout"`
	CWD    *string           `json:"cwd"`
}

type Node struct {
	Uid     string            `json:"uid"`
	Cmds    []Cmd             `json:"cmds"`
	Inputs  []string          `json:"inputs"`
	Outputs []string          `json:"outputs"`
	Deps    []string          `json:"deps"`
	KV      map[string]string `json:"kv"`
	Env     map[string]string `json:"env"`
}

type Proto struct {
	Graph  []Node   `json:"graph"`
	Result []string `json:"result"`
}

func loads[T any](data []byte) *T {
	var res T

	throw(json.Unmarshal(data, &res))

	return &res
}

func dumps[T any](obj *T) []byte {
	res, err := json.Marshal(*obj)

	throw(err)

	return res
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
	RC    *RenderContext
	Wait  atomic.Uint64
	Done  atomic.Uint64
}

func (self *Executor) executeNode(node *Node) {
	self.Sched.acquire()

	defer self.Sched.release()

	for _, o := range node.Outputs {
		os.MkdirAll(filepath.Dir(o), os.ModePerm)
	}

	for _, c := range node.Cmds {
		cmd := exec.Command(c.Args[0], c.Args[1:]...)

		if c.CWD == nil {
			cmd.Dir = self.RC.SrcRoot
		} else {
			cmd.Dir = *c.CWD
		}

		res, err := cmd.CombinedOutput()

		if err != nil {
			os.Stdout.Write(res)
			fmtException("%s: %v", c.Args, err).throw()
		}

		if c.StdOut == nil {
			os.Stdout.Write(res)
		} else {
			throw(ioutil.WriteFile(*c.StdOut, res, 0666))
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

func (self *Executor) execute(template *Node) {
	node := self.RC.mountNode(template)

	if complete(node) {
		return
	}

	self.Wait.Add(1)

	defer self.Done.Add(1)

	self.visitAll(node.Deps)
	self.executeNode(node)

	if !complete(node) {
		fmtException("node %s not complete", node).throw()
	}

	done := self.Done.Load() + 1
	wait := self.Wait.Load()

	fmt.Printf("[%s] {%d/%d} %s\n", color(node.KV["pc"], node.KV["p"]), done, wait, template.Outputs)
}

func newNodeFuture(ex *Executor, node *Node) *Future {
	return &Future{F: func() {
		ex.execute(node)
	}}
}

func newExecutor(nodes []Node, threads int, rc *RenderContext) *Executor {
	deps := map[string]*Future{}

	res := &Executor{
		ByUid: &deps,
		Sched: newSemaphore(threads),
		RC:    rc,
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

func calcHostArch() string {
	if runtime.GOARCH == "amd64" {
		return "x86_64"
	}

	if runtime.GOARCH == "arm64" {
		if runtime.GOOS == "darwin" {
			return "arm64"
		}

		if runtime.GOOS == "ios" {
			return "arm64"
		}

		return "aarch64"
	}

	return runtime.GOARCH
}

func calcHostPlatform() string {
	return runtime.GOOS + "-" + calcHostArch()
}

func merge(t []Node, h []Node) []Node {
	return t
}

func (self *Flags) parseInto(kv string) {
	fields := strings.Split(kv, "=")

	if len(fields) == 1 {
		(*self)[fields[0]] = "yes"
	} else if len(fields) == 2 {
		(*self)[fields[0]] = fields[1]
	} else {
		fmtException("malformed flag %s, %s", kv).throw()
	}
}

func async[T any](f func() T) func() T {
	ch := make(chan T)

	go func() {
		ch <- f()
	}()

	return func() T {
		return <-ch
	}
}

func handleMake(args []string) {
	rc := newRenderContext()

	hp := "default-" + calcHostPlatform()

	tflags := Flags{
		"GG_BUILD_TYPE":      "release",
		"GG_TARGET_PLATFORM": hp,
		"USER_CFLAGS":        os.Getenv("CFLAGS"),
		"USER_CONLYFLAGS":    os.Getenv("CONLYFLAGS"),
		"USER_CXXFLAGS":      os.Getenv("CXXFLAGS"),
		"USER_LDFLAGS":       os.Getenv("LDFLAGS"),
	}

	hflags := Flags{
		"GG_BUILD_TYPE":      "release",
		"GG_TARGET_PLATFORM": hp,
	}

	state := getopt.NewState(args)

	config := getopt.Config{
		Opts:     getopt.OptStr("GrdkTD:j:"),
		LongOpts: getopt.LongOptStr("keep-going,dump-graph,release,debug,target-platform:,host-platform:,hpf:"),
		Mode:     getopt.ModeInOrder,
		Func:     getopt.FuncGetOptLong,
	}

	targets := []string{}
	keep := false
	threads := 1
	dump := false

	for opt, err := range state.All(config) {
		if err == getopt.ErrDone {
			break
		}

		throw(err)

		if opt.Char == 'k' || opt.Name == "keep-going" {
			keep = true
		} else if opt.Char == 'G' || opt.Name == "dump-graph" {
			dump = true
		} else if opt.Char == 'T' {
			//pass
		} else if opt.Char == 'D' {
			tflags.parseInto(opt.OptArg)
		} else if opt.Char == 'j' {
			i, err := strconv.Atoi(opt.OptArg)
			throw(err)
			threads = i
		} else if opt.Char == 1 {
			targets = append(targets, opt.OptArg)
		} else if opt.Char == 'r' || opt.Name == "release" {
			tflags["GG_BUILD_TYPE"] = "release"
		} else if opt.Char == 'd' || opt.Name == "debug" {
			tflags["GG_BUILD_TYPE"] = "debug"
		} else if opt.Name == "target-platform" {
			tflags["GG_TARGET_PLATFORM"] = opt.OptArg
		} else if opt.Name == "host-platform" {
			hflags["GG_TARGET_PLATFORM"] = opt.OptArg
		} else if opt.Name == "hpf" {
			hflags.parseInto(opt.OptArg)
		} else {
			fmtException("unhandled flag %s", opt.Char).throw()
		}
	}

	gen := func(flags Flags) *Proto {
		tc := rc.toolChainFor(flags)
		conf := rc.genConfFor(tc)

		return loads[Proto](rc.genGraphFor(conf, targets, keep))
	}

	// scatter
	tasync := async(func() *Proto {
		return gen(tflags)
	})

	hasync := async(func() *Proto {
		return gen(hflags)
	})

	// gather
	tproto := tasync()
	hproto := hasync()

	// merge
	graph := merge(tproto.Graph, hproto.Graph)

	if dump {
		os.Stdout.Write(dumps(&graph))
	}

	if threads > 0 {
		newExecutor(graph, threads, rc).visitAll(tproto.Result)
	}
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
		exc.fatal(1, "fatal error")
	})
}
