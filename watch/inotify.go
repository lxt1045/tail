// Copyright (c) 2015 HPE Software Inc. All rights reserved.
// Copyright (c) 2013 ActiveState Software Inc. All rights reserved.

package watch

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/hpcloud/tail/util"

	"gopkg.in/fsnotify/fsnotify.v1"
	"gopkg.in/tomb.v1"
)

// InotifyFileWatcher uses inotify to monitor file changes.
type InotifyFileWatcher struct {
	Filename string
	Size     int64
}

func NewInotifyFileWatcher(filename string) *InotifyFileWatcher {
	fw := &InotifyFileWatcher{filepath.Clean(filename), 0}
	return fw
}

func (fw *InotifyFileWatcher) BlockUntilExists(t *tomb.Tomb) error {
	err := WatchCreate(fw.Filename)
	if err != nil {
		return err
	}
	defer RemoveWatchCreate(fw.Filename)

	// Do a real check now as the file might have been created before
	// calling `WatchFlags` above.
	if _, err = os.Stat(fw.Filename); !os.IsNotExist(err) {
		// file exists, or stat returned an error.
		return err
	}

	events := Events(fw.Filename)

	for {
		select {
		case evt, ok := <-events:
			if !ok {
				return fmt.Errorf("inotify watcher has been closed")
			}
			evtName, err := filepath.Abs(evt.Name)
			if err != nil {
				return err
			}
			fwFilename, err := filepath.Abs(fw.Filename)
			if err != nil {
				return err
			}
			if evtName == fwFilename {
				return nil
			}
		case <-t.Dying():
			return tomb.ErrDying
		}
	}
	panic("unreachable")
}

// 如果 inotify 消息有问题，则定时检查file stats
func (fw *InotifyFileWatcher) ChangeEvents(t *tomb.Tomb, pos int64) (*FileChanges, error) {
	origF, err := os.Open(fw.Filename)
	// origFi, err := os.Stat(fw.Filename)
	if err != nil {
		return nil, err
	}
	origFi, err := origF.Stat()
	if err != nil {
		return nil, err
	}
	err = Watch(fw.Filename)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
		}
	}()

	changes := NewFileChanges()
	fw.Size = pos

	go func() {

		ticker := time.NewTicker(time.Duration(time.Second * 1))
		defer ticker.Stop()
		events := Events(fw.Filename)
		prevSize, pollPrevSize := fw.Size, fw.Size
		prevModTime := origFi.ModTime()

		for {

			var evt fsnotify.Event
			var ok bool

			select {
			case evt, ok = <-events:
				if !ok {
					RemoveWatch(fw.Filename)
					return
				}
			case <-t.Dying():
				RemoveWatch(fw.Filename)
				return
			case <-ticker.C:
			}

			switch {
			case !ok: // ticker 超时; 没有收到inotify消息
				fi, err := os.Stat(fw.Filename)
				if err != nil {
					// Windows cannot delete a file if a handle is still open (tail keeps one open)
					// so it gives access denied to anything trying to read it until all handles are released.
					if os.IsNotExist(err) || (runtime.GOOS == "windows" && os.IsPermission(err)) {
						// File does not exist (has been deleted).
						RemoveWatch(fw.Filename)
						changes.NotifyDeleted()
						return
					}

					// XXX: report this error back to the user
					util.Fatal("Failed to stat file %v: %v", fw.Filename, err)
				}

				// File got moved/renamed?
				if !os.SameFile(origFi, fi) {
					RemoveWatch(fw.Filename)
					changes.NotifyDeleted()
					return
				}

				// File got truncated?
				fw.Size = fi.Size()
				if pollPrevSize > 0 && pollPrevSize > fw.Size {
					changes.NotifyTruncated()
					pollPrevSize = fw.Size
					continue
				}
				// File got bigger?
				if pollPrevSize > 0 && pollPrevSize < fw.Size {
					changes.NotifyModified()
					pollPrevSize = fw.Size
					continue
				}
				pollPrevSize = fw.Size

				// File was appended to (changed)?
				modTime := fi.ModTime()
				if modTime != prevModTime {
					prevModTime = modTime
					changes.NotifyModified()
				}
			case evt.Op&fsnotify.Remove == fsnotify.Remove:
				fallthrough

			case evt.Op&fsnotify.Rename == fsnotify.Rename:
				RemoveWatch(fw.Filename)
				changes.NotifyDeleted()
				return

			//With an open fd, unlink(fd) - inotify returns IN_ATTRIB (==fsnotify.Chmod)
			case evt.Op&fsnotify.Chmod == fsnotify.Chmod:
				fallthrough

			case evt.Op&fsnotify.Write == fsnotify.Write:
				// fi, err := os.Stat(fw.Filename)
				fi, err := origF.Stat()
				if err != nil {
					if os.IsNotExist(err) {
						RemoveWatch(fw.Filename)
						changes.NotifyDeleted()
						return
					}
					// XXX: report this error back to the user
					util.Fatal("Failed to stat file %v: %v", fw.Filename, err)
				}
				fw.Size = fi.Size()

				if prevSize > 0 && prevSize > fw.Size {
					changes.NotifyTruncated()
				} else {
					changes.NotifyModified()
				}
				prevSize = fw.Size
			}
		}
	}()

	return changes, nil
}
