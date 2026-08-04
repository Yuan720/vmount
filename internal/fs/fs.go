package fs

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Yuan720/vmount/internal/cache"
	"github.com/Yuan720/vmount/internal/config"
	"github.com/Yuan720/vmount/internal/storage"
	"github.com/winfsp/cgofuse/fuse"
)

type Fs struct {
	fuse.FileSystemBase
	client    storage.Backend
	blocks    *cache.BlockCache
	dirs      *cache.DirCache
	metas     *cache.MetaCache
	spool     *cache.Spool
	cm        *caseMap
	handles   *handleTable
	chunkSize int64
	exclude   map[string]bool
	usePH     bool
	uploadWG  sync.WaitGroup
	uploadMx  sync.Map
	refreshMx sync.Map
	refreshAt sync.Map
	cacheDir  string
}

func (f *Fs) uploadMuFor(path string) *sync.Mutex {
	mu, _ := f.uploadMx.LoadOrStore(path, &sync.Mutex{})
	return mu.(*sync.Mutex)
}

const refreshMinInterval = 5 * time.Second

func (f *Fs) beginRefresh(path string) bool {
	_, loaded := f.refreshMx.LoadOrStore(path, struct{}{})
	return !loaded
}

func (f *Fs) endRefresh(path string) {
	f.refreshMx.Delete(path)
}

// canRefresh applies a per-path throttle (used by open/Readdir-triggered
// refreshes). Write-triggered refreshes bypass it via refreshDirNow.
func (f *Fs) canRefresh(path string) bool {
	if t, ok := f.refreshAt.Load(path); ok {
		if ts, ok := t.(time.Time); ok && time.Since(ts) < refreshMinInterval {
			return false
		}
	}
	f.refreshAt.Store(path, time.Now())
	return true
}

func (f *Fs) refreshDir(path string) {
	if !f.canRefresh(path) {
		return
	}
	if !f.beginRefresh(path) {
		return
	}
	defer f.endRefresh(path)
	f.doRefreshDir(path)
}

func (f *Fs) refreshDirNow(path string) {
	if !f.beginRefresh(path) {
		return
	}
	defer f.endRefresh(path)
	f.doRefreshDir(path)
}

func (f *Fs) doRefreshDir(path string) {
	entries, err := f.client.List(context.Background(), path)
	if err != nil {
		return
	}
	f.dirs.Set(path, entries)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name)
		f.metas.Set(joinPath(path, e.Name), storage.Meta{
			Size:    e.Size,
			ModTime: e.ModTime,
			IsDir:   e.IsDir,
		})
	}
	f.cm.Update(path, names)
	f.saveCache()
	debugf("refreshDir %q -> %d entries", path, len(entries))
}

func (f *Fs) refreshMeta(path string) {
	if !f.canRefresh(path) {
		return
	}
	if !f.beginRefresh(path) {
		return
	}
	defer f.endRefresh(path)
	f.doRefreshMeta(path)
}

func (f *Fs) doRefreshMeta(path string) {
	meta, err := f.client.Stat(context.Background(), path)
	if err != nil {
		return
	}
	f.metas.Set(path, *meta)
	f.saveCache()
	debugf("refreshMeta %q size=%d isdir=%v", path, meta.Size, meta.IsDir)
}

func New(client storage.Backend, cfg *config.Config) (*Fs, error) {
	spool, err := cache.NewSpool(filepath.Join(cfg.CacheDir, "spool"))
	if err != nil {
		return nil, err
	}
	chunk := cfg.ChunkSize
	if chunk <= 0 {
		chunk = 8 * 1024 * 1024
	}
	exclude := suffixSet(cfg.ExcludeSuffixes)
	f := &Fs{
		client:    client,
		blocks:    cache.NewBlockCache(cfg.ReadCacheMB * 1024 * 1024),
		dirs:      cache.NewDirCache(time.Duration(cfg.ListTTLSeconds) * time.Second),
		metas:     cache.NewMetaCache(time.Duration(cfg.ListTTLSeconds) * time.Second),
		spool:     spool,
		cm:        newCaseMap(),
		handles:   newHandleTable(),
		chunkSize: chunk,
		exclude:   exclude,
		usePH:     cfg.UsePlaceholder,
		cacheDir:  cfg.CacheDir,
	}
	f.loadCache()
	return f, nil
}

func (f *Fs) loadCache() {
	f.dirs.Load(filepath.Join(f.cacheDir, "dircache.json"))
	f.metas.Load(filepath.Join(f.cacheDir, "metacache.json"))
}

func (f *Fs) saveCache() {
	f.dirs.Save(filepath.Join(f.cacheDir, "dircache.json"))
	f.metas.Save(filepath.Join(f.cacheDir, "metacache.json"))
}

func suffixSet(suffixes []string) map[string]bool {
	m := map[string]bool{}
	for _, s := range suffixes {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" {
			continue
		}
		if !strings.HasPrefix(s, ".") {
			s = "." + s
		}
		m[s] = true
	}
	return m
}

func (f *Fs) isExcluded(path string) bool {
	if len(f.exclude) == 0 {
		return false
	}
	return f.exclude[strings.ToLower(filepath.Ext(path))]
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

func joinPath(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

func (f *Fs) fillStat(stat *fuse.Stat_t, meta *storage.Meta) {
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

func (f *Fs) currentMeta(path string) (*storage.Meta, error) {
	if size, mt, ok := f.spool.SizeOf(f.spoolKey(path)); ok {
		return &storage.Meta{Size: size, ModTime: mt}, nil
	}
	if meta, ok, stale := f.metas.Get(path); ok && !stale {
		return &meta, nil
	}
	meta, err := f.client.Stat(context.Background(), path)
	if err != nil {
		return nil, err
	}
	f.metas.Set(path, *meta)
	return meta, nil
}

func (f *Fs) asyncUpload(key, path string) {
	f.uploadWG.Add(1)
	defer f.uploadWG.Done()
	mu := f.uploadMuFor(path)
	mu.Lock()
	defer mu.Unlock()
	entry, err := f.spool.Open(key)
	if err != nil {
		debugf("asyncUpload %q open err: %v", path, err)
		return
	}
	rs, err := entry.SeekReader()
	if err != nil {
		f.spool.Close(key)
		debugf("asyncUpload %q seek err: %v", path, err)
		return
	}
	size := entry.Size()
	err = f.client.Put(context.Background(), path, rs, size, f.chunkSize)
	f.spool.Close(key)
	if err != nil {
		debugf("asyncUpload %q Put err: %v (spool kept)", path, err)
		return
	}
	f.spool.Remove(key)
	f.invalidatePath(path)
	go f.refreshDirNow(f.parentDir(path))
	debugf("asyncUpload %q done size=%d", path, size)
}

func (f *Fs) Init() {
	if r := mapUid0ToCurrentUser(); r != 0 {
		fmt.Fprintf(os.Stderr, "winfsp uidmap init failed: %d\n", r)
	}
}

func (f *Fs) WaitUploads() {
	f.uploadWG.Wait()
}

func (f *Fs) SaveCache() {
	f.saveCache()
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
	debugf("Getattr %q fh=%d", path, fh)
	path = f.norm(path)
	if h := f.handles.Get(fh); h != nil {
		path = h.path
	}
	if path == "" {
		f.fillStat(stat, &storage.Meta{IsDir: true})
		return 0
	}
	if size, mt, ok := f.spool.SizeOf(f.spoolKey(path)); ok {
		f.fillStat(stat, &storage.Meta{Size: size, ModTime: mt})
		return 0
	}
	meta, ok, stale := f.metas.Get(path)
	if !ok {
		m, err := f.client.Stat(context.Background(), path)
		if err != nil {
			if err == storage.ErrNotFound {
				debugf("Getattr %q -> ENOENT", path)
				return -fuse.ENOENT
			}
			debugf("Getattr %q err: %v", path, err)
			return -fuse.EIO
		}
		f.metas.Set(path, *m)
		f.fillStat(stat, m)
		return 0
	}
	if stale {
		go f.refreshMeta(path)
	}
	f.fillStat(stat, &meta)
	return 0
}

func (f *Fs) Readdir(path string, fill func(name string, stat *fuse.Stat_t, off int64) bool, off int64, fh uint64) int {
	path = f.norm(path)
	entries, ok, _ := f.dirs.Get(path)
	if !ok {
		var err error
		entries, err = f.client.List(context.Background(), path)
		if err != nil {
			return -fuse.EIO
		}
		f.dirs.Set(path, entries)
	} else {
		go f.refreshDir(path)
	}
	names := make([]string, 0, len(entries)+2)
	names = append(names, ".", "..")
	for _, e := range entries {
		names = append(names, e.Name)
		f.metas.Set(joinPath(path, e.Name), storage.Meta{
			Size:    e.Size,
			ModTime: e.ModTime,
			IsDir:   e.IsDir,
		})
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
	debugf("Opendir %q", path)
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
			if err == storage.ErrNotFound {
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
	debugf("Create %q flags=%#o", path, flags)
	path = f.norm(path)
	if err := f.resetSpool(f.spoolKey(path)); err != nil {
		debugf("Create %q resetSpool err: %v", path, err)
		return -fuse.EIO, ^uint64(0)
	}
	f.metas.Set(path, storage.Meta{Size: 0, ModTime: time.Now()})
	if !f.isExcluded(path) {
		go f.createEmpty(path)
	}
	f.dirs.Invalidate(f.parentDir(path))
	fh := f.handles.Add(&handle{path: path, write: true, spooled: true})
	debugf("Create %q -> fh=%d", path, fh)
	go f.refreshDirNow(f.parentDir(path))
	return 0, fh
}

// createEmpty creates a 0-byte object in the background so the file becomes
// visible on the remote side shortly after creation. It shares the per-path
// upload lock with asyncUpload so the later content upload always wins.
func (f *Fs) createEmpty(path string) {
	f.uploadWG.Add(1)
	defer f.uploadWG.Done()
	mu := f.uploadMuFor(path)
	mu.Lock()
	defer mu.Unlock()
	if err := f.client.Put(context.Background(), path, strings.NewReader(""), 0, f.chunkSize); err != nil {
		debugf("createEmpty %q err: %v", path, err)
		return
	}
	debugf("createEmpty %q done", path)
	go f.refreshDirNow(f.parentDir(path))
}

func (f *Fs) Read(path string, buff []byte, off int64, fh uint64) int {
	path = f.norm(path)
	if h := f.handles.Get(fh); h != nil {
		path = h.path
	}
	meta, err := f.currentMeta(path)
	if err != nil {
		if err == storage.ErrNotFound {
			return 0
		}
		debugf("Read %q off=%d meta err: %v", path, off, err)
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
			debugf("Read %q off=%d blk=%d GetRange err: %v", path, off, blkStart, gerr)
			if n > 0 {
				return n
			}
			return -fuse.EIO
		}
		data, gerr = io.ReadAll(io.LimitReader(rc, size))
		rc.Close()
		if gerr != nil {
			debugf("Read %q off=%d blk=%d readall err: %v", path, off, blkStart, gerr)
			if n > 0 {
				return n
			}
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
		debugf("Flush %q fh=%d skip", path, fh)
		return 0
	}
	key := f.spoolKey(h.path)
	if h.deleted {
		f.spool.Remove(key)
		h.spooled = false
		return 0
	}
	if f.isExcluded(h.path) {
		h.spooled = false
		return 0
	}
	h.spooled = false
	if f.spool.Exists(key) {
		go f.asyncUpload(key, h.path)
	}
	return 0
}

func (f *Fs) Fsync(path string, datasync bool, fh uint64) int {
	return f.Flush(path, fh)
}

func (f *Fs) Release(path string, fh uint64) int {
	h := f.handles.Remove(fh)
	if h == nil || !h.spooled {
		debugf("Release %q fh=%d", path, fh)
		return 0
	}
	key := f.spoolKey(h.path)
	if h.deleted {
		f.spool.Remove(key)
		return 0
	}
	if f.isExcluded(h.path) {
		return 0
	}
	if f.spool.Exists(key) {
		go f.asyncUpload(key, h.path)
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
		if err == storage.ErrNotFound {
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
	debugf("Unlink %q", path)
	path = f.norm(path)
	key := f.spoolKey(path)
	f.handles.MarkDeleted(path)
	f.spool.Remove(key)
	if err := f.client.Remove(context.Background(), path); err != nil {
		debugf("Unlink %q Remove err: %v", path, err)
		return -fuse.EIO
	}
	f.invalidatePath(path)
	go f.refreshDirNow(f.parentDir(path))
	return 0
}

func (f *Fs) Mkdir(path string, mode uint32) int {
	debugf("Mkdir %q", path)
	path = f.norm(path)
	if f.usePH {
		if err := f.client.PutPlaceholder(context.Background(), path); err != nil {
			debugf("Mkdir %q placeholder err: %v", path, err)
			return -fuse.EIO
		}
	}
	f.dirs.Invalidate(f.parentDir(path))
	f.metas.Set(path, storage.Meta{IsDir: true, ModTime: time.Now()})
	go f.refreshDirNow(f.parentDir(path))
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
	if f.usePH {
		if err := f.client.RemovePlaceholder(context.Background(), path); err != nil {
			return -fuse.EIO
		}
	}
	f.invalidatePath(path)
	go f.refreshDirNow(f.parentDir(path))
	return 0
}

func (f *Fs) Rename(oldpath string, newpath string) int {
	debugf("Rename %q -> %q", oldpath, newpath)
	oldpath = f.norm(oldpath)
	newpath = f.norm(newpath)
	if f.isExcluded(oldpath) {
		key := f.spoolKey(oldpath)
		if !f.spool.Exists(key) {
			debugf("Rename temp %q no spool", oldpath)
			return -fuse.ENOENT
		}
		newKey := f.spoolKey(newpath)
		if err := f.spool.Move(key, newKey); err != nil {
			debugf("Rename temp %q spool move err: %v", oldpath, err)
			return -fuse.EIO
		}
		f.invalidatePath(oldpath)
		f.invalidatePath(newpath)
		go f.asyncUpload(newKey, newpath)
		go f.refreshDirNow(f.parentDir(newpath))
		debugf("Rename temp %q -> %q async upload started", oldpath, newpath)
		return 0
	}
	meta, err := f.client.Stat(context.Background(), oldpath)
	if err != nil {
		if err == storage.ErrNotFound {
			debugf("Rename %q -> %q ENOENT", oldpath, newpath)
			return -fuse.ENOENT
		}
		debugf("Rename %q Stat err: %v", oldpath, err)
		return -fuse.EIO
	}
	if !meta.IsDir {
		if err := f.client.Copy(context.Background(), oldpath, newpath); err != nil {
			debugf("Rename %q Copy err: %v", oldpath, err)
			return -fuse.EIO
		}
		if err := f.client.Remove(context.Background(), oldpath); err != nil {
			debugf("Rename %q Remove err: %v", oldpath, err)
			return -fuse.EIO
		}
	} else {
		rels, lerr := f.client.ListRecursive(context.Background(), oldpath)
		if lerr != nil {
			debugf("Rename dir %q ListRecursive err: %v", oldpath, lerr)
			return -fuse.EIO
		}
		for _, rel := range rels {
			if rel == "" {
				if f.usePH {
					if cerr := f.client.CopyPlaceholder(context.Background(), oldpath, newpath); cerr != nil {
						debugf("Rename dir %q CopyPlaceholder err: %v", oldpath, cerr)
						return -fuse.EIO
					}
				}
			} else {
				if cerr := f.client.Copy(context.Background(), oldpath+"/"+rel, newpath+"/"+rel); cerr != nil {
					debugf("Rename dir %q Copy %q err: %v", oldpath, rel, cerr)
					return -fuse.EIO
				}
			}
		}
		for _, rel := range rels {
			if rel == "" {
				if f.usePH {
					f.client.RemovePlaceholder(context.Background(), oldpath)
				}
			} else {
				f.client.Remove(context.Background(), oldpath+"/"+rel)
			}
		}
	}
	f.invalidatePath(oldpath)
	f.invalidatePath(newpath)
	go f.refreshDirNow(f.parentDir(oldpath))
	go f.refreshDirNow(f.parentDir(newpath))
	return 0
}

func (f *Fs) Utimens(path string, tmsp []fuse.Timespec) int {
	return 0
}

func (f *Fs) Chmod(path string, mode uint32) int {
	return 0
}
