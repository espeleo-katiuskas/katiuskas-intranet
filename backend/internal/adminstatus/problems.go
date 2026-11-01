package adminstatus

import (
	"fmt"
	"os"
	"syscall"
)

// diskStatus is one filesystem that holds some of the directories in Storage.
// Directories on the same filesystem are one entry, since they fill up together.
type diskStatus struct {
	Paths       []string `json:"paths"`
	TotalBytes  uint64   `json:"total_bytes"`
	FreeBytes   uint64   `json:"free_bytes"`
	UsedPercent int      `json:"used_percent"`
}

// disksOf measures the filesystem of each storage directory that exists.
func disksOf(storage []storageStatus) []diskStatus {
	// Never nil: with no directory to measure the JSON has an empty list, not null.
	disks := []diskStatus{}
	byDevice := map[uint64]int{}
	for _, dir := range storage {
		if !dir.Exists {
			continue
		}
		info, err := os.Stat(dir.Path)
		if err != nil {
			continue
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			continue
		}
		device := uint64(st.Dev)
		if i, seen := byDevice[device]; seen {
			disks[i].Paths = append(disks[i].Paths, dir.Path)
			continue
		}
		var fs syscall.Statfs_t
		if err := syscall.Statfs(dir.Path, &fs); err != nil {
			continue
		}
		byDevice[device] = len(disks)
		disks = append(disks, diskOf(dir.Path, &fs))
	}
	return disks
}

// diskOf computes what `df` shows: the percentage is of what a normal user can use, so that the
// blocks reserved for root do not make a full disk look as if it had room.
func diskOf(path string, fs *syscall.Statfs_t) diskStatus {
	size := uint64(fs.Bsize)
	used := (fs.Blocks - fs.Bfree) * size
	available := fs.Bavail * size
	percent := 0
	if used+available > 0 {
		percent = int((used*100 + used + available - 1) / (used + available)) // rounded up, like df
	}
	return diskStatus{
		Paths:       []string{path},
		TotalBytes:  fs.Blocks * size,
		FreeBytes:   available,
		UsedPercent: percent,
	}
}

// Problem is one thing that is wrong. Key identifies it regardless of the details in its text
// (a disk going from 91 % to 92 % is the same problem), so the monitor can tell a new problem
// from one it has already reported.
type Problem struct {
	Key  string
	Text string
}

// storageLabels name the directories the way the interface does.
var storageLabels = map[string]string{
	"library":            "el archivo",
	"user_files":         "los ficheros de las personas",
	"activity_resources": "las memorias de actividades",
	"transaction_files":  "los ficheros de las transacciones",
}

// problemsOf lists what in a status is wrong: the database not answering, a directory that does
// not exist, a disk at or above diskAlertPercent (0 turns the disk check off).
func problemsOf(s statusResponse, diskAlertPercent int) []Problem {
	var problems []Problem
	if !s.Database.OK {
		problems = append(problems, Problem{"database", "La base de datos no responde: " + s.Database.Error})
	}
	for _, dir := range s.Storage {
		if !dir.Exists {
			label := storageLabels[dir.Name]
			if label == "" {
				label = dir.Name
			}
			problems = append(problems, Problem{
				"storage:" + dir.Name,
				fmt.Sprintf("No existe el directorio de %s (%s)", label, dir.Path),
			})
		}
	}
	if diskAlertPercent > 0 {
		for _, disk := range s.Disks {
			if disk.UsedPercent >= diskAlertPercent {
				problems = append(problems, Problem{
					"disk:" + disk.Paths[0],
					fmt.Sprintf("Disco casi lleno (%d %% usado, quedan %s): %s",
						disk.UsedPercent, humanBytes(disk.FreeBytes), disk.Paths[0]),
				})
			}
		}
	}
	return problems
}

func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
