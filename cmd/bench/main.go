// Command bench times scans without the GUI.
//
//	bench [-n runs] [dir]           full scans
//	bench -save cache.bin [dir]     full scan, then save the tree
//	bench -update cache.bin         load a saved tree and apply FSEvents changes
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"time"

	"diskpy/internal/fsevents"
	"diskpy/internal/scan"
)

func main() {
	runs := flag.Int("n", 1, "number of runs")
	flag.IntVar(&scan.Workers, "w", scan.Workers, "concurrent workers")
	prof := flag.String("cpuprofile", "", "write CPU profile to file")
	save := flag.String("save", "", "save the scanned tree to this file")
	update := flag.String("update", "", "load a tree from this file and update it")
	flag.Parse()
	root := flag.Arg(0)
	if root == "" {
		root, _ = os.UserHomeDir()
	}
	root, _ = filepath.EvalSymlinks(root)
	if *prof != "" {
		f, _ := os.Create(*prof)
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}

	if *update != "" {
		runUpdate(*update)
		return
	}
	for range *runs {
		var p scan.Progress
		id := fsevents.Now()
		start := time.Now()
		n, err := scan.Scan(context.Background(), root, &p)
		check(err)
		elapsed := time.Since(start)
		runtime.GC()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		fmt.Printf("%v  files=%d size=%d errors=%d heap=%dMB\n", elapsed.Round(time.Millisecond), n.Files(), n.Size(), p.Errors.Load(), ms.HeapAlloc>>20)
		if *save != "" {
			saveTree(*save, n, id)
		}
	}
}

func runUpdate(file string) {
	start := time.Now()
	f, err := os.Open(file)
	check(err)
	n, since, err := scan.Load(f)
	f.Close()
	check(err)
	fmt.Printf("load     %v  files=%d size=%d\n", time.Since(start).Round(time.Millisecond), n.Files(), n.Size())

	t := time.Now()
	id := fsevents.Now()
	changes, err := fsevents.Since(n.Name, since, 10*time.Second)
	check(err)
	fmt.Printf("fsevents %v  changes=%d\n", time.Since(t).Round(time.Millisecond), len(changes))

	t = time.Now()
	var p scan.Progress
	check(scan.Rescan(context.Background(), n, changes, &p))
	fmt.Printf("rescan   %v  files=%d size=%d relisted-files=%d\n", time.Since(t).Round(time.Millisecond), n.Files(), n.Size(), p.Files.Load())
	fmt.Printf("total    %v\n", time.Since(start).Round(time.Millisecond))
	saveTree(file, n, id)
}

func saveTree(file string, n *scan.Node, id uint64) {
	start := time.Now()
	f, err := os.Create(file)
	check(err)
	check(scan.Save(f, n, id))
	check(f.Close())
	st, _ := os.Stat(file)
	fmt.Printf("save     %v  %dMB\n", time.Since(start).Round(time.Millisecond), st.Size()>>20)
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
