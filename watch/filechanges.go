package watch

import "time"

type FileChanges struct {
	Modified  chan bool // Channel to get notified of modifications
	Truncated chan bool // Channel to get notified of truncations
	Deleted   chan bool // Channel to get notified of deletions/renames
}

func NewFileChanges() *FileChanges {
	return &FileChanges{
		make(chan bool, 1), make(chan bool, 1), make(chan bool, 1)}
}

func (fc *FileChanges) NotifyModified() {
	sendOnlyIfEmpty(fc.Modified)
}

func (fc *FileChanges) NotifyTruncated() {
	fc.NotifyModified() // 先检查是否有数据可读，再发送删除信号
	time.Sleep(time.Millisecond * 100)
	sendOnlyIfEmpty(fc.Truncated)
}

func (fc *FileChanges) NotifyDeleted() {
	fc.NotifyModified() // 先检查是否有数据可读，再发送删除信号
	time.Sleep(time.Millisecond * 100)
	sendOnlyIfEmpty(fc.Deleted)
}

// sendOnlyIfEmpty sends on a bool channel only if the channel has no
// backlog to be read by other goroutines. This concurrency pattern
// can be used to notify other goroutines if and only if they are
// looking for it (i.e., subsequent notifications can be compressed
// into one).
func sendOnlyIfEmpty(ch chan bool) {
	select {
	case ch <- true:
	default:
	}
}
