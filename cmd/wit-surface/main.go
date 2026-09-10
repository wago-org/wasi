// Command wit-surface emits the imported function surface declared by a WIT
// tree. It is intentionally small and dependency-free so the checked-in WASI
// 0.2.0 completeness golden can be reproduced from the pinned upstream tag.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	packageRE   = regexp.MustCompile(`^package ([^;]+);`)
	interfaceRE = regexp.MustCompile(`^interface ([a-z0-9-]+) \{`)
	resourceRE  = regexp.MustCompile(`^resource ([a-z0-9-]+) \{`)
	functionRE  = regexp.MustCompile(`^%?([a-z][a-z0-9-]*): func\(`)
)

func main() {
	root := flag.String("root", ".", "root of the pinned WIT tree")
	exclude := flag.String("exclude", "wasi:cli/run@0.2.0", "comma-separated interface IDs to omit")
	excludePrefix := flag.String("exclude-prefix", "wasi:http/", "comma-separated interface ID prefixes to omit")
	flag.Parse()
	excluded := map[string]bool{}
	for _, x := range strings.Split(*exclude, ",") {
		excluded[x] = true
	}
	prefixes := strings.Split(*excludePrefix, ",")
	packages := map[string]string{}
	_ = filepath.WalkDir(*root, func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(name) != ".wit" {
			return err
		}
		f, e := os.Open(name)
		if e != nil {
			return e
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			if m := packageRE.FindStringSubmatch(strings.TrimSpace(scanner.Text())); m != nil {
				packages[filepath.Dir(name)] = m[1]
				break
			}
		}
		f.Close()
		return scanner.Err()
	})
	var out []string
	err := filepath.WalkDir(*root, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(name) != ".wit" {
			return nil
		}
		entries, e := scan(name, packages[filepath.Dir(name)], excluded, prefixes)
		out = append(out, entries...)
		return e
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	sort.Strings(out)
	for _, line := range out {
		fmt.Println(line)
	}
}

func scan(name, pkg string, excluded map[string]bool, prefixes []string) ([]string, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var iface, resource string
	depth, ifaceDepth, resourceDepth := 0, 0, 0
	var out []string
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if strings.HasPrefix(line, "//") {
			continue
		}
		if m := packageRE.FindStringSubmatch(line); m != nil {
			pkg = m[1]
		}
		if m := interfaceRE.FindStringSubmatch(line); m != nil {
			iface = m[1]
			ifaceDepth = depth + 1
		}
		if iface != "" {
			parts := strings.Split(pkg, "@")
			if len(parts) != 2 {
				continue
			}
			id := parts[0] + "/" + iface + "@" + parts[1]
			skip := excluded[id]
			for _, prefix := range prefixes {
				if prefix != "" && strings.HasPrefix(id, prefix) {
					skip = true
				}
			}
			if !skip {
				if m := resourceRE.FindStringSubmatch(line); m != nil {
					resource = m[1]
					resourceDepth = depth + 1
				}
				if m := functionRE.FindStringSubmatch(line); m != nil {
					name := strings.TrimPrefix(m[1], "%")
					if resource != "" {
						name = "[method]" + resource + "." + name
					}
					out = append(out, id+"#"+name)
				}
			}
		}
		depth += strings.Count(line, "{") - strings.Count(line, "}")
		if resource != "" && depth < resourceDepth {
			resource = ""
		}
		if iface != "" && depth < ifaceDepth {
			iface = ""
		}
	}
	return out, s.Err()
}
