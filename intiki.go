/*
 * intiki is free software; you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation; either version 2 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program; if not, write to the Free Software
 * Foundation, Inc., 51 Franklin St, Fifth Floor, Boston, MA  02110-1301  USA
 *
 * As a special exception, you may use this file as part of a free software
 * library without restriction.  Specifically, if other files instantiate
 * templates or use macros or inline functions from this file, or you compile
 * this file and link it with other files to produce an executable, this
 * file does not by itself cause the resulting executable to be covered by
 * the GNU General Public License.  This exception does not however
 * invalidate any other reasons why the executable file might be covered by
 * the GNU General Public License.
 *
 * Copyright 2016-2018 Tokita, Hiroshi
 */

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"strconv"
	"syscall"
)

type Command struct {
	Stage string `json:"stage"`
	Recipe string `json:"recipe"`
	Source string `json:"source"`
	Target string `json:"target"`
	Flags []string `json:"flags"`
	BuildPath string `json:"build_path"`
	CorePath string `json:"core_path"`
	SystemPath string `json:"system_path"`
	VariantPath string `json:"variant_path"`
	ProjectName string `json:"project_name"`
	ArchiveFile string `json:"archive_file"`
	SerialPort string `json:"serial_port"`
}

func Verbose(level int, format string, args ...interface{}) {
	DebugLog(fmt.Sprintf(format, args...))
	if(level <= verbose) {
		fmt.Fprintf(os.Stderr, format, args...);
	}
}

func write_file(file string, buf []byte) (int, error) {
	fp, err := os.OpenFile(file, syscall.O_RDWR | syscall.O_CREAT, 0755)
	if err != nil { return 0, err }
	defer fp.Close()

	n, err := fp.Write(buf)
	if err != nil { return 0, err }

	return n, nil
}

func encode_to_file(file string, ifc interface{}) (int, error) {
	buf, err := json.MarshalIndent(ifc, "", " ")
	if err != nil { return 0, err }
	return write_file(file, buf)
}

func decode_from_file(file string, ifc interface{}) error {
	_, err := os.Stat(file)
	if os.IsNotExist(err) { return err }

	fp, err := os.Open(file)
	if err != nil { return err }
	defer fp.Close()

	r := bufio.NewReader(fp)
	dec := json.NewDecoder(r)
	dec.Decode(&ifc)

	//Verbose(5, "decode_from_file: %v\n", ifc)
	return nil
}

func select_command(slc []Command, f func(s Command) bool) []Command {
	ans := make([]Command, 0)
	for _, x := range slc {
		if f(x) == true {
			ans = append(ans, x)
		}
	}
	return ans
}

func collect_string(slc []Command, f func(s Command) string) []string {
	ans := make([]string, 0)
	for _, x := range slc {
	ans = append(ans, f(x))
	}
	return ans
}

func contains(s []string, e string) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}

func format_makefile(template string, replace map[string]string) string {
	Verbose(6, "template: %v\n", template)
	Verbose(6, "replace: %v\n", replace)

	out := ""
	_, err := os.Stat(template)
	if os.IsNotExist(err) { errors.New(err.Error() ) }

	fp, err := os.Open(template)
	if err != nil { errors.New(err.Error() ) }
	defer fp.Close()

	scanner := bufio.NewScanner(fp)
	for scanner.Scan() {
		line := scanner.Text()
		rep := regexp.MustCompile(`(###\s*<<<)([^>\s]*)(>>>\s*###)`)
		matches := rep.FindAllStringSubmatch(line,-1)

		if matches != nil {
			found := false

			for k := range replace {
				if(k == matches[0][2]) {
					out = out + rep.ReplaceAllString(line, replace[k]) + "\n"
					found = true
				}
			}
			if(found) {
				continue
			}
		}
		out = out + line + "\n"
	}
	if err := scanner.Err(); err != nil {
		panic(err)
	}

	//Verbose(5, out)
	return out
}

func ToMsysSlash(p string) string {
	s := filepath.ToSlash(p)
	if len(s) < 4 {
		return s
	}
	if (s[1:3] == ":/") {
		ss := "/"
		ss += strings.ToLower(s[0:1])
		ss += s[2:]
		return ss
	}
	return s
}

func GetLineOfFile(filename string, lineno int) (string, error) {
	f, err := os.Open(filename)
	if err != nil {
		Verbose(0, "File %s could not read: %v\n", filename, err)
		return "", err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)

	count := 1
	for scanner.Scan() {
		if count == lineno {
			return scanner.Text(), nil
		}
		count += 1
	}

	if serr := scanner.Err(); serr != nil {
		Verbose(0, "File %s scan error: %v\n", filename, err)
		return "", serr
	}

	return "", errors.New("not found")
}

func PrintErrIncludeLine(reader io.Reader) error {
	scanner := bufio.NewScanner(reader)

	INCLUDE_REGEXP := regexp.MustCompile(`(?ms)^\\s*#[ \t]*include\\s*[<\"](\\S+)[\">]`)
	NOSUCHFILE_REGEXP := regexp.MustCompile(`(.*):([0-9]*):([0-9]*): fatal error:.*: No such file or directory$`)

	var file string
	var lineno int
	var column int
	var matches [][]string
	var err error
	prev_match_no_suchfile := false
	for scanner.Scan() {
		line := scanner.Text()

		matches = NOSUCHFILE_REGEXP.FindAllStringSubmatch(line, -1)
		if matches != nil {
			file = matches[0][1]
			lineno, err = strconv.Atoi(matches[0][2])
			if err != nil {
				return err
			}
			column, err = strconv.Atoi(matches[0][3])
			if err != nil {
				return err
			}
			prev_match_no_suchfile = true
		} else {
			matches := INCLUDE_REGEXP.FindAllStringSubmatch(line, -1)
			if matches == nil && prev_match_no_suchfile {
				incline, errx := GetLineOfFile(file, lineno)
				if errx == nil {
					fmt.Fprintf(os.Stderr, " " + incline + "\n")
					fmt.Fprintf(os.Stderr, strings.Repeat(" ", column) + "^\n")
				}
			}
			prev_match_no_suchfile = false
		}
		fmt.Fprintf(os.Stderr, line)
		fmt.Fprintf(os.Stderr, "\n")
		DebugLog(line)
	}

	return nil
}

func ExecCommand(ostrm io.Writer, estrm io.Writer, exe string, args... string) int {
	path := os.Getenv("PATH")
	if(cmds_path != "") {
		path = cmds_path + string(os.PathListSeparator) + path
	}
	if(compiler_path != "") {
		path = compiler_path + string(os.PathListSeparator) + path
	}
	if(uploader_path != "") {
		path = uploader_path + string(os.PathListSeparator) + path
	}
	os.Setenv("PATH", path)
	os.Setenv("LANG", "C")


	path = os.Getenv("PATH")
	Verbose(6, "PATH=" + path + "\n")
	Verbose(0, exe + " " + strings.Join(args, " ") + "\n")

	cmd := exec.Command(exe, args...)
	cmd.Stdout = ostrm
	cmd.Stderr = estrm

	err := cmd.Run()

	var exitStatus int
	if err != nil {
		if e2, ok := err.(*exec.ExitError); ok {
			if s, ok := e2.Sys().(syscall.WaitStatus); ok {
				exitStatus = s.ExitStatus()
			} else {
				panic(errors.New("Unimplemented for system where exec.ExitError.Sys() is not syscall.WaitStatus."))
			}
		}
	} else {
		exitStatus = 0
	}

	if exitStatus != 0 || verbose > 6 {
		Verbose(0, "exec.Command.Run err=%d\n", exitStatus)
	}

	return exitStatus;
}

var (
	recipe string
	source string
	target string
	build_path string
	core_path string
	system_path string
	variant_path string
	platform_path string
	project_name string
	archive_file string
	serial_port string
	stage string
	template string
	makefile string
	variant_name string
	platform_version string

	contiki_target_main string
	cmds_path string
	compiler_path string
	uploader_path string
	includes string
	make_command string
	make_processnum int
	verbose int
	woff bool
	wall bool
	wextra bool
	version bool
)

func main() {

	flag.StringVar(&build_path,		"build.path", "",		"same as platform.txt")
	flag.StringVar(&core_path,		"build.core.path", "",		"same as platform.txt")
	flag.StringVar(&system_path,		"build.system.path", "",	"same as platform.txt")
	flag.StringVar(&variant_path,		"build.variant.path", "",	"same as platform.txt")
	flag.StringVar(&platform_path,		"runtime.platform.path", "",	"same as platform.txt")
	flag.StringVar(&variant_name,		"build.variant", "",		"same as platform.txt")
	flag.StringVar(&project_name,		"project_name", "",		"same as platform.txt")
	flag.StringVar(&archive_file,		"archive_file", "",		"same as platform.txt")
	flag.StringVar(&serial_port,		"serial.port", "",		"same as platform.txt")
	flag.StringVar(&recipe,			"recipe", "",			"recipe")
	flag.StringVar(&stage,			"stage", "",			"build stage")
	flag.StringVar(&target,			"target", "",			"target file")
	flag.StringVar(&source,			"source", "",			"source file")
	flag.StringVar(&template,		"template", "",			"Makefile template")
	flag.StringVar(&makefile,		"makefile", "",			"Makefile name")
	flag.StringVar(&cmds_path,		"build.usr.bin.path", "",	"target object")
	flag.StringVar(&compiler_path,		"build.compiler.path", "",	"cpmpiler path")
	flag.StringVar(&uploader_path,		"build.uploader.path", "",	"uploader path")
	flag.StringVar(&contiki_target_main,	"contiki.target.main", "",	"CONTIKI_TARGET_MAIN")
	flag.StringVar(&platform_version,	"platform.version", "",		"version")
	flag.StringVar(&includes,		"includes", "",			"includes")
	flag.StringVar(&make_command,		"make.command", "make",		"make command executable")
	flag.IntVar(&make_processnum,		"make.processnum", -1,		"make process number")
	flag.IntVar(&verbose,			"verbose", -1,			"verbose level")
	flag.BoolVar(&woff,			"w", false,			"verbose level")
	flag.BoolVar(&wall,			"Wall", false,			"verbose level")
	flag.BoolVar(&wextra,			"Wextra", false,		"verbose level")
	flag.BoolVar(&version,			"version", false,		"show program version")
	flag.Parse()

	flags := flag.Args()

	if version == true {
		fmt.Println("intiki " + GetVersion() )
		fmt.Println("Copyright (C) 2016-2018 Tokita, Hiroshi")
		fmt.Println("https://github.com/soburi/intiki")

	}

	if verbose == -1 {
		verbose = 3
		if woff == true {
			verbose = 0
		}
		if wall == true {
			verbose = 5
		}
		if wextra == true {
			verbose = 9
		}
	}

	Verbose(6, "recipe:%s stage:%s target:%s source:%s\n", recipe,stage,target,source)

	genmf := strings.Replace(strings.TrimPrefix(target, build_path), "\\", "_", -1);
	if recipe == "ar" {
		genmf = genmf + strings.Replace("_" + strings.TrimPrefix(source, build_path), "\\", "_", -1);
	}
	genmf = strings.Replace(genmf, "/", "_", -1);
	genmf = strings.Replace(genmf, ":", "_", -1);
	genmf = build_path + string(os.PathSeparator) + genmf + "." + recipe + ".genmf"

	if recipe == "cpp.o" || recipe == "c.o" || recipe == "S.o" || recipe == "ar" || recipe == "ld" {
		cmd := Command{	stage, recipe, source, target, flags,
				build_path, core_path, system_path, variant_path,
				project_name, archive_file, serial_port }

		stgfile := build_path + string(os.PathSeparator) + "genmf.stage"
		_, err := os.Stat(stgfile)
		if !os.IsNotExist(err) {
			decode_from_file(stgfile, &stage);
			cmd.Stage = stage
		}

		_, err = encode_to_file(genmf, cmd)
		if err != nil { errors.New(err.Error() ) }

	} else if recipe == "stage" {
		genmf = build_path + string(os.PathSeparator) + "genmf.stage"

		_, err := encode_to_file(genmf, stage)
		if err != nil { errors.New(err.Error() ) }

	} else if recipe == "echo" {
		fmt.Println(strings.Join(flags, " ") )

	} else if recipe == "genprjc" {
		rep := regexp.MustCompile(`\.ino$`)
		if !rep.MatchString(project_name) {
			rep = regexp.MustCompile(`\.pde$`)
		}
		prjc := build_path + string(os.PathSeparator) + rep.ReplaceAllString(project_name, ".c")

		_, err := os.Stat(prjc)
		if os.IsNotExist(err) {
			f, err2 := os.Create(prjc)
			if err2 != nil { panic(err2) }
			defer f.Close()
			f.Write(([]byte)(""))
		}

	} else if recipe == "make" {
		makeflags := os.Getenv("MAKEFLAGS")

		if make_processnum == -1 {
			numcores := os.Getenv("NUMBER_OF_PROCESSORS")
			makeflags = makeflags + " -j" + numcores
		} else {
			makeflags = makeflags + " -j" + strconv.Itoa(make_processnum)
		}
		os.Setenv("MAKEFLAGS", makeflags)

		sys_args := []string{}

		if(serial_port != "") {
			switch runtime.GOOS {
			case "windows":
				rep := regexp.MustCompile(`^(COM)([0-9]*)(\s*)$`)
				matches := rep.FindAllStringSubmatch(serial_port,-1)
				sys_args = append(sys_args, "USBDEVBASENAME=" + matches[0][1])
				sys_args = append(sys_args, "MOTE=" + matches[0][2])
			case "linux":
				rep := regexp.MustCompile(`^([^0-9]*)([0-9]*)$`)
				matches := rep.FindAllStringSubmatch(serial_port,-1)

				sys_args = append(sys_args, "USBDEVBASENAME=" + matches[0][1])
				sys_args = append(sys_args, "MOTE=" + matches[0][2])
			case "darwin":
				rep := regexp.MustCompile(`^(/dev/.*-)(.*)(\s*)$`)
				matches := rep.FindAllStringSubmatch(serial_port,-1)

				devname := strings.Replace(matches[0][1], "/dev/cu.usbserial", "/dev/tty.usbserial", -1)
				sys_args = append(sys_args, "USBDEVBASENAME=" + devname)
				sys_args = append(sys_args, "MOTE=" + matches[0][2])
			}
		}

		os.Setenv("ZEPHYR_BASE", system_path + "\\zephyr")

		args := append(sys_args, flags...)

		exitStatus := ExecCommand(os.Stdout, os.Stderr, make_command, args...)
		os.Exit(exitStatus)

	} else if recipe == "preproc.includes" || recipe == "preproc.macros" {

		args := append([]string{ "-s", "-C", ToMsysSlash(build_path)})

		verbose = 0

		includes := []string{}

		flaggroup := ""
		for _, f := range flags {
			if f == "-includes" || f == "-make-args" {
				flaggroup = f
				continue
			}

			if flaggroup == "-make-args" {
				args = append(args, f)
				continue
			} else if flaggroup == "-includes" {
				if strings.HasPrefix(f,"-I") {
					f = "-I" + ToMsysSlash(f[2:])
				}
				includes = append(includes, f)
			}
			includes = append(includes, f)
		}

		args = append(args, recipe)

		replace_map := map[string]string {}

		preprocfile := build_path + string(os.PathSeparator) + "genmf.preproc"
		_, err := os.Stat(preprocfile)
		if !os.IsNotExist(err) {
			decode_from_file(preprocfile, &replace_map);
		}

		replace_map["ARDUINO_SYSTEM_PATH"] = ToMsysSlash(system_path)
		replace_map["ARDUINO_VARIANT_PATH"] = ToMsysSlash(variant_path)
		if(recipe == "preproc.includes") {
			replace_map["ARDUINO_PREPROC_INCLUDES_FLAGS"]  = "\t" + strings.Join(includes, " ")
			replace_map["ARDUINO_PREPROC_INCLUDES_SOURCE"] = "\t" + ToMsysSlash(source)
			replace_map["ARDUINO_PREPROC_INCLUDES_OUTFILE"] = "\t" + ToMsysSlash(target)
		} else {
			replace_map["ARDUINO_PREPROC_MACROS_FLAGS"]    = "\t" + strings.Join(includes, " ")
			replace_map["ARDUINO_PREPROC_MACROS_SOURCE"]   = "\t" + ToMsysSlash(source)
			replace_map["ARDUINO_PREPROC_MACROS_OUTFILE"]   = "\t" + ToMsysSlash(target)
		}

		_, err = encode_to_file(preprocfile, replace_map)

		out := format_makefile(template, replace_map)

		makefilename := makefile
		if makefile == "" {
			makefilename = strings.Replace(path.Base(template), ".template", "", -1)
		}

		os.Remove(build_path  + string(os.PathSeparator) + makefilename)
		write_file(build_path + string(os.PathSeparator) + makefilename, []byte(out))

		var bufStdErr bytes.Buffer
		var bufStdOut bytes.Buffer

		DebugLog(make_command + " " + strings.Join(args, " "))

		exitStatus := ExecCommand(&bufStdOut, &bufStdErr, make_command, args...)

		if exitStatus != 0{
			Verbose(3, "%s error %v", recipe, err)
			DebugLog( fmt.Sprint("%s error %v", recipe, err) )
		}

		scanner := bufio.NewScanner(&bufStdOut)
		for scanner.Scan() {
			line := scanner.Text()
			DebugLog(line)
		}

		err = PrintErrIncludeLine(&bufStdErr)

		os.Exit(exitStatus)

	} else if recipe == "makefile" {
		genmfs, _ := filepath.Glob(build_path + string(os.PathSeparator) + "*.genmf")
		commands := make([]Command, 0)
		for _, f := range genmfs {
			cmd := Command{}
			decode_from_file(f, &cmd);
			commands = append(commands, cmd)
		}

		cores_srcs := func() string {
			cores_srcs := select_command(commands, func (c Command) bool {
				return (strings.HasSuffix(c.Recipe, ".o") && c.Stage == "core" && strings.HasPrefix(c.Source, core_path) )
			} )

			cores_list := collect_string(cores_srcs, func (c Command) string { return ToMsysSlash(c.Source) } )
			return ("\t" + strings.Join(cores_list, " \\\n\t") + "\n")
		}

		variant_srcs := func() string {
			var_srcs := select_command(commands, func (c Command) bool {
				return (strings.HasSuffix(c.Recipe, ".o") && c.Stage == "core" && strings.HasPrefix(c.Source, variant_path) )
			} )

			var_list := collect_string(var_srcs, func (c Command) string { return ToMsysSlash(c.Source) } )
			return ("\t" + strings.Join(var_list, " \\\n\t") + "\n")
		}

		libs_srcs := func() string {
			libcmds := select_command(commands, func (c Command) bool {
				return (strings.HasSuffix(c.Recipe, ".o") && c.Stage == "libraries")
			})

			libs_srcs := collect_string(libcmds, func (c Command) string { return ToMsysSlash(c.Source) } )
			return ("\t" + strings.Join(libs_srcs, " \\\n\t") + "\n")
		}

		sketch_srcs := func() string {
			sketches := select_command(commands, func (c Command) bool {
				return (strings.HasSuffix(c.Recipe, ".o") && c.Stage == "sketch")
			} )

			sketchs_list := collect_string(sketches, func (c Command) string { return ToMsysSlash(c.Source) } )
			return ("\t" + strings.Join(sketchs_list, " \\\n\t") + "\n")
		}

		sketch_flags := func() string {
			flgs:= []string{}

			libcmds := select_command(commands, func (c Command) bool {
				return (strings.HasSuffix(c.Recipe, ".o") && c.Stage == "sketch")
			})


			for _, cmd := range libcmds {
				for _, flg := range cmd.Flags {
					if !contains(flgs, flg) {
						if (strings.HasPrefix(flg, "-I") || strings.HasPrefix(flg, "-L") ) {
							flg = flg[0:2] + ToMsysSlash(flg[2:])
						}
						flgs = append(flgs, flg)
					}
				}
			}

			return strings.Join(flgs, " ")
		}

		ldcmd := select_command(commands, func (c Command) bool {
			return (c.Recipe == "ld")
		})[0]

		replace_map := map[string]string {}

		preprocfile := build_path + string(os.PathSeparator) + "genmf.preproc"
		_, err := os.Stat(preprocfile)
		if !os.IsNotExist(err) {
			decode_from_file(preprocfile, &replace_map);
		}

		replace_map["ARDUINO_CFLAGS"] = sketch_flags()
		replace_map["ARDUINO_PROJECT_NAME"] = ToMsysSlash(ldcmd.ProjectName)
		replace_map["ARDUINO_SYSTEM_PATH"] = ToMsysSlash(ldcmd.SystemPath)
		replace_map["ARDUINO_BUILD_PATH"] = ToMsysSlash(ldcmd.BuildPath)
		replace_map["ARDUINO_CORE_PATH"] = ToMsysSlash(ldcmd.CorePath)
		replace_map["ARDUINO_VARIANT_PATH"] = ToMsysSlash(ldcmd.VariantPath)
		replace_map["ARDUINO_ARCHIVE_FILE"] = ToMsysSlash(ldcmd.ArchiveFile)
		replace_map["ARDUINO_CORES_SRCS"] = cores_srcs()
		replace_map["ARDUINO_VARIANT_SRCS"] = variant_srcs()
		replace_map["ARDUINO_LIBRARIES_SRCS"] = libs_srcs()
		replace_map["ARDUINO_SKETCH_SRCS"] = sketch_srcs()
		replace_map["ARDUINO_VARIANT"] = variant_name
		replace_map["ARDUINO_PLATFORM_VERSION"] = platform_version

		out := format_makefile(template, replace_map)

		makefilename := makefile
		makedir := path.Dir(makefile)
		DebugLog(makedir)
		if makefile == "" {
			makefilename = build_path + strings.Replace(path.Base(template), ".template", "", -1)
		} else if makedir == "" {
			makedir = build_path
		}
		os.MkdirAll(makedir, os.ModePerm)

		DebugLog(makefilename)
		os.Remove(makefilename)
		write_file(makefilename, []byte(out))

		if verbose < 10  {
			for _, f := range genmfs {
				os.Remove(f)
			}
			os.Remove(build_path + string(os.PathSeparator) + "genmf.stage")
			os.Remove(build_path + string(os.PathSeparator) + "genmf.preproc")
		}
	}
}
