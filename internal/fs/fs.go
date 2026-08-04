package fs

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Yuan720/vmount/internal/cache"
	"github.com/Yuan720/vmount/internal/config"
	"github.com/Yuan720/vmount/internal/s3client"
	"github.com/winfsp/cgofuse/fuse"
)

type Fs struct {
	fuse.FileSystemBase
	client    *s3client.Client
	blocks    *cache.BlockCache
	dirs      *cache.DirCache
	metas     *cache.MetaCache
	spool     *cache.Spool
	cm        *caseMap
	handles   *handleTable
	chunkSize int64
}

func New(client *s3client.Client, cfg *config.Config) (*Fs, error) {
	spool, err := cache.NewSpool(filepath.Join(cfg.CacheDir, "spool"))
	if err != nil {
		return nil, err
	}
	chunk := cfg.ChunkSize
	if chunk <= 0 {
		chunk = 8 * 1024 * 1024
	}
	return &Fs{
		client:    client,
		blocks:    cache.NewBlockCache(cfg.ReadCacheMB * 1024 * 1024),
		dirs:      cache.NewDirCache(time.Duration(cfg.ListTTLSeconds) * time.Second),
		metas:     cache.NewMetaCache(time.Duration(cfg.ListTTLSeconds) * time.Second),
		spool:     spool,
		cm:        newCaseMap(),
		handles:   newHandleTable(),
		chunkSize: chunk,
	}, nil
}

func (f *Fs) norm(path string) string {
	return f.cm.Resolve(path)
}

func (f *Fs) parentDir(path string) string {
	path = strings.Trim(path, "/")
	idx := strings.LastIndex(path, "/")
	if idx < 0 {
		return ""
	}
	return path[:idx]
}

func (f *Fs) fillStat(stat *fuse.Stat_t, meta *s3client.Meta) {
	if meta.IsDir {
		stat.Mode = fuse.S_IFDIR | 0o777
		stat.Nlink = 2
	} else {
		stat.Mode = fuse.S_IFREG | 0o666
		stat.Nlink = 1
		stat.Size = meta.Size
	}
	stat.Blksize = 4096
	stat.Blocks = (meta.Size + 4095) / 4096
	t := meta.ModTime
	ts := fuse.Timespec{Sec: t.Unix(), Nsec: int64(t.Nanosecond())}
	stat.Atim = ts
	stat.Mtim = ts
	stat.Ctim = ts
	stat.Birthtim = ts
}

func (f *Fs) invalidatePath(path string) {
	f.dirs.Invalidate(f.parentDir(path))
	f.metas.Invalidate(path)
	f.blocks.InvalidatePrefix(path)
}

func (f *Fs) spoolKey(path string) string {
	return "spool:" + path
}

func (f *Fs) currentMeta(path string) (*s3client.Meta, error) {
	if size, mt, ok := f.spool.SizeOf(f.spoolKey(path)); ok {
		return &s3client.Meta{Size: size, ModTime: mt}, nil
	}
	if meta, ok := f.metas.Get(path); ok {
		return &meta, nil
	}
	meta, err := f.client.Stat(context.Background(), path)
	if err != nil {
		return nil, err
	}
	f.metas.Set(path, *meta)
	return meta, nil
}

func (f *Fs) upload(h *handle) int {
	key := f.spoolKey(h.path)
	if !f.spool.Exists(key) {
		return 0
	}
	if h.deleted {
		f.spool.Remove(key)
		h.spooled = false
		return 0
	}
	entry, err := f.spool.Open(key)
	if err != nil {
		return -fuse.EIO
	}
	rs, err := entry.SeekReader()
	if err != nil {
		f.spool.Close(key)
		return -fuse.EIO
	}
	size := entry.Size()
	err = f.client.Put(context.Background(), h.path, rs, size, f.chunkSize)
	f.spool.Close(key)
	if err != nil {
		return -fuse.EIO
	}
	f.spool.Remove(key)
	h.spooled = false
	f.invalidatePath(h.path)
	return 0
}

func (f *Fs) Init() {
	if r := mapUid0ToCurrentUser(); r != 0 {
		fmt.Fprintf(os.Stderr, "winfsp uidmap init failed: %d\n", r)
	}
}

func (f *Fs) Statfs(path string, statfs *fuse.Statfs_t) int {
	const blockSize = 4096
	const totalBlocks = uint64(1) << 32
	statfs.Bsize = blockSize
	statfs.Frsize = blockSize
	statfs.Blocks = totalBlocks
	statfs.Bfree = totalBlocks
	statfs.Bavail = totalBlocks
	statfs.Files = 1 << 20
	statfs.Ffree = 1 << 20
	statfs.Namemax = 255
	return 0
}

func (f *Fs) Getattr(path string, stat *fuse.Stat_t, fh uint64) int {
	path = f.norm(path)
	if h := f.handles.Get(fh); h != nil {
		path = h.path
	}
	if path == "" {
		f.fillStat(stat, &s3client.Meta{IsDir: true})
		return 0
	}
	meta, err := f.currentMeta(path)
	if err != nil {
		if err == s3client.ErrNotFound {
			return -fuse.ENOENT
		}
		return -fuse.EIO
	}
	f.fillStat(stat, meta)
	return 0
}

func (f *Fs) Readdir(path string, fill func(name string, stat *fuse.Stat_t, off int64) bool, off int64, fh uint64) int {
	path = f.norm(path)
	entries, ok := f.dirs.Get(path)
	if !ok {
		var err error
		entries, err = f.client.List(context.Background(), path)
		if err != nil {
			return -fuse.EIO
		}
		f.dirs.Set(path, entries)
	}
	names := make([]string, 0, len(entries)+2)
	names = append(names, ".", "..")
	for _, e := range entries {
		names = append(names, e.Name)
	}
	f.cm.Update(path, names[2:])
	for _, n := range names {
		if !fill(n, nil, 0) {
			break
		}
	}
	return 0
}

func (f *Fs) Opendir(path string) (int, uint64) {
	fh := f.handles.Add(&handle{path: f.norm(path)})
	return 0, fh
}

func (f *Fs) Releasedir(path string, fh uint64) int {
	f.handles.Remove(fh)
	return 0
}

func (f *Fs) Open(path string, flags int) (int, uint64) {
	path = f.norm(path)
	write := flags&fuse.O_ACCMODE != fuse.O_RDONLY
	if write && flags&fuse.O_CREAT == 0 {
		meta, err := f.currentMeta(path)
		if err != nil {
			if err == s3client.ErrNotFound {
				return -fuse.ENOENT, ^uint64(0)
			}
			return -fuse.EIO, ^uint64(0)
		}
		if meta.IsDir {
			return -fuse.EISDIR, ^uint64(0)
		}
		if flags&fuse.O_TRUNC == 0 && meta.Size > 0 {
			if rc := f.loadToSpool(path, meta.Size); rc != 0 {
				return rc, ^uint64(0)
			}
		}
	}
	if write && flags&fuse.O_TRUNC != 0 {
		if err := f.resetSpool(f.spoolKey(path)); err != nil {
			return -fuse.EIO, ^uint64(0)
		}
		f.invalidatePath(path)
		fh := f.handles.Add(&handle{path: path, write: true, spooled: true})
		return 0, fh
	}
	fh := f.handles.Add(&handle{path: path, write: write})
	return 0, fh
}

func (f *Fs) loadToSpool(path string, size int64) int {
	rc, _, gerr := f.client.GetRange(context.Background(), path, 0, size)
	if gerr != nil {
		return -fuse.EIO
	}
	key := f.spoolKey(path)
	entry, oerr := f.spool.Open(key)
	if oerr != nil {
		rc.Close()
		return -fuse.EIO
	}
	_, cerr := io.Copy(entry, rc)
	rc.Close()
	f.spool.Close(key)
	if cerr != nil {
		return -fuse.EIO
	}
	return 0
}

func (f *Fs) resetSpool(key string) error {
	f.spool.Remove(key)
	entry, err := f.spool.Open(key)
	if err != nil {
		return err
	}
	if err := entry.Truncate(0); err != nil {
		f.spool.Close(key)
		return err
	}
	f.spool.Close(key)
	return nil
}

func (f *Fs) Create(path string, flags int, mode uint32) (int, uint64) {
	path = f.norm(path)
	if err := f.resetSpool(f.spoolKey(path)); err != nil {
		return -fuse.EIO, ^uint64(0)
	}
	f.metas.Invalidate(path)
	f.dirs.Invalidate(f.parentDir(path))
	fh := f.handles.Add(&handle{path: path, write: true, spooled: true})
	return 0, fh
}

func (f *Fs) Read(path string, buff []byte, off int64, fh uint64) int {
	path = f.norm(path)
	if h := f.handles.Get(fh); h != nil {
		path = h.path
	}
	meta, err := f.currentMeta(path)
	if err != nil {
		if err == s3client.ErrNotFound {
			return 0
		}
		return -fuse.EIO
	}
	if off >= meta.Size {
		return 0
	}
	if int64(len(buff)) > meta.Size-off {
		buff = buff[:meta.Size-off]
	}
	key := f.spoolKey(path)
	if f.spool.Exists(key) {
		entry, oerr := f.spool.Open(key)
		if oerr == nil {
			n, rerr := entry.ReadAt(buff, off)
			f.spool.Close(key)
			if rerr != nil && rerr != io.EOF {
				return -fuse.EIO
			}
			return n
		}
	}
	n := 0
	for len(buff) > 0 {
		blkStart := off / f.chunkSize * f.chunkSize
		blkKey := fmt.Sprintf("%s#%d", path, blkStart)
		data, ok := f.blocks.Get(blkKey, meta.ModTime)
		if !ok {
			size := f.chunkSize
			if blkStart+size > meta.Size {
				size = meta.Size - blkStart
			}
			rc, _, gerr := f.client.GetRange(context.Background(), path, blkStart, size)
			if gerr != nil {
				return -fuse.EIO
			}
			data, gerr = io.ReadAll(io.LimitReader(rc, size))
			rc.Close()
			if gerr != nil {
				return -fuse.EIO
			}
			f.blocks.Put(blkKey, meta.ModTime, data)
		}
		rel := off - blkStart
		if rel >= int64(len(data)) {
			break
		}
		c := copy(buff, data[rel:])
		n += c
		off += int64(c)
		buff = buff[c:]
	}
	return n
}

func (f *Fs) Write(path string, buff []byte, off int64, fh uint64) int {
	h := f.handles.Get(fh)
	if h == nil {
		return -fuse.EBADF
	}
	key := f.spoolKey(h.path)
	entry, err := f.spool.Open(key)
	if err != nil {
		return -fuse.EIO
	}
	n, werr := entry.WriteAt(buff, off)
	f.spool.Close(key)
	if werr != nil {
		return -fuse.EIO
	}
	h.spooled = true
	return n
}

func (f *Fs) Flush(path string, fh uint64) int {
	h := f.handles.Get(fh)
	if h == nil || !h.spooled {
		return 0
	}
	return f.upload(h)
}

func (f *Fs) Fsync(path string, datasync bool, fh uint64) int {
	return f.Flush(path, fh)
}

func (f *Fs) Release(path string, fh uint64) int {
	h := f.handles.Remove(fh)
	if h != nil && h.spooled {
		return f.upload(h)
	}
	return 0
}

func (f *Fs) Truncate(path string, size int64, fh uint64) int {
	path = f.norm(path)
	key := f.spoolKey(path)
	if !f.spool.Exists(key) {
		if err := f.prepareSpool(path, size); err != 0 {
			return err
		}
	}
	entry, err := f.spool.Open(key)
	if err != nil {
		return -fuse.EIO
	}
	terr := entry.Truncate(size)
	f.spool.Close(key)
	if terr != nil {
		return -fuse.EIO
	}
	if h := f.handles.Get(fh); h != nil {
		h.spooled = true
	}
	f.invalidatePath(path)
	return 0
}

func (f *Fs) prepareSpool(path string, size int64) int {
	meta, err := f.client.Stat(context.Background(), path)
	if err != nil {
		if err == s3client.ErrNotFound {
			return 0
		}
		return -fuse.EIO
	}
	if meta.Size == 0 || size == 0 {
		return 0
	}
	if size < meta.Size {
		return f.loadToSpool(path, size)
	}
	return f.loadToSpool(path, meta.Size)
}

func (f *Fs) Unlink(path string) int {
	path = f.norm(path)
	key := f.spoolKey(path)
	f.handles.MarkDeleted(path)
	f.spool.Remove(key)
	if err := f.client.Remove(context.Background(), path); err != nil {
		return -fuse.EIO
	}
	f.invalidatePath(path)
	return 0
}

func (f *Fs) Mkdir(path string, mode uint32) int {
	path = f.norm(path)
	if err := f.client.PutPlaceholder(context.Background(), path); err != nil {
		return -fuse.EIO
	}
	f.dirs.Invalidate(f.parentDir(path))
	f.metas.Invalidate(path)
	return 0
}

func (f *Fs) Rmdir(path string) int {
	path = f.norm(path)
	entries, err := f.client.List(context.Background(), path)
	if err != nil {
		return -fuse.EIO
	}
	if len(entries) > 0 {
		return -fuse.ENOTEMPTY
	}
	if err := f.client.RemovePlaceholder(context.Background(), path); err != nil {
		return -fuse.EIO
	}
	f.invalidatePath(path)
	return 0
}

func (f *Fs) Rename(oldpath string, newpath string) int {
	oldpath = f.norm(oldpath)
	newpath = f.norm(newpath)
	if err := f.client.Copy(context.Background(), oldpath, newpath); err != nil {
		return -fuse.EIO
	}
	if err := f.client.Remove(context.Background(), oldpath); err != nil {
		return -fuse.EIO
	}
	f.invalidatePath(oldpath)
	f.invalidatePath(newpath)
	return 0
}

func (f *Fs) Utimens(path string, tmsp []fuse.Timespec) int {
	return 0
}

func (f *Fs) Chmod(path string, mode uint32) int {
	return 0
}
