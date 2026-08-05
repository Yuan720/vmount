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
	active    sync.Map
	cacheDir  string
}

func baseName(path string) string {
	path = strings.Trim(path, "/")
	idx := strings.LastIndex(path, "/")
	if idx < 0 {
		return path
	}
	return path[idx+1:]
}

func (f *Fs) activeAdd(dir, name string) {
	m, _ := f.active.LoadOrStore(dir, &sync.Map{})
	m.(*sync.Map).Store(name, struct{}{})
}

func (f *Fs) activeRemove(dir, name string) {
	if m, ok := f.active.Load(dir); ok {
		m.(*sync.Map).Delete(name)
	}
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
		if err == storage.ErrNotFound {
			if m, ok, _ := f.metas.Get(path); ok && time.Since(m.ModTime) > 5*time.Second {
				f.metas.Invalidate(path)
				debugf("refreshMeta %q -> gone, cache cleared", path)
			} else {
				debugf("refreshMeta %q -> gone but recent create, kept visible", path)
			}
		}
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
	if meta, ok, _ := f.metas.Get(path); ok {
		return &meta, nil
	}
	go f.refreshMeta(path)
	return &storage.Meta{}, nil
}

func (f *Fs) asyncUpload(key, path string) {
	f.uploadWG.Add(1)
	defer f.uploadWG.Done()
	if !f.spool.Exists(key) {
		debugf("asyncUpload %q skipped (spool gone)", path)
		return
	}
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
	if size == 0 {
		f.spool.Close(key)
		f.spool.Remove(key)
		debugf("asyncUpload %q skipped (empty placeholder, kept visible locally)", path)
		return
	}
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
	if ok {
		// 0B placeholder (not yet synced to S3): visible for a couple of
		// seconds so the browser can confirm creation, then treated as gone
		// so a later download of the same name does not trigger the
		// "already exists" prompt.
		if meta.Size == 0 && !meta.IsDir && time.Since(meta.ModTime) > 60*time.Second {
			go f.refreshMeta(path)
			debugf("Getattr %q -> placeholder expired", path)
			return -fuse.ENOENT
		}
		if stale {
			go f.refreshMeta(path)
		}
		f.fillStat(stat, &meta)
		return 0
	}
	go f.refreshMeta(path)
	debugf("Getattr %q -> optimistic ENOENT", path)
	return -fuse.ENOENT
}

func (f *Fs) Readdir(path string, fill func(name string, stat *fuse.Stat_t, off int64) bool, off int64, fh uint64) int {
	path = f.norm(path)
	entries, ok, stale := f.dirs.Get(path)
	if !ok || stale {
		newEntries, err := f.client.List(context.Background(), path)
		if err != nil {
			if !ok {
				return -fuse.EIO
			}
			debugf("Readdir %q refresh failed, using stale cache", path)
		} else {
			entries = newEntries
			f.dirs.Set(path, entries)
		}
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
		if flags&fuse.O_TRUNC == 0 && meta.Size > 0 && !f.isExcluded(path) {
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
	f.dirs.Invalidate(f.parentDir(path))
	fh := f.handles.Add(&handle{path: path, write: true, spooled: true})
	debugf("Create %q -> fh=%d", path, fh)
	go f.refreshDirNow(f.parentDir(path))
	return 0, fh
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
	if f.isExcluded(path) {
		// Excluded files are never uploaded to S3; reading an empty spool
		// must return EOF rather than failing against the remote.
		return 0
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
		debugf("Write %q fh=%d off=%d len=%d -> EBADF", path, fh, off, len(buff))
		return -fuse.EBADF
	}
	key := f.spoolKey(h.path)
	entry, err := f.spool.Open(key)
	if err != nil {
		debugf("Write %q fh=%d off=%d len=%d spool open err: %v", h.path, fh, off, len(buff), err)
		return -fuse.EIO
	}
	n, werr := entry.WriteAt(buff, off)
	size := entry.Size()
	f.spool.Close(key)
	if werr != nil {
		debugf("Write %q fh=%d off=%d len=%d WriteAt err: %v", h.path, fh, off, len(buff), werr)
		return -fuse.EIO
	}
	h.spooled = true
	f.metas.Set(h.path, storage.Meta{Size: size, ModTime: time.Now()})
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
	meta, _ := f.currentMeta(path)
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
	f.metas.Invalidate(path)
	f.dirs.Invalidate(f.parentDir(path))
	go f.doUnlink(path)
	return 0
}

func (f *Fs) doUnlink(path string) {
	f.uploadWG.Add(1)
	defer f.uploadWG.Done()
	if err := f.client.Remove(context.Background(), path); err != nil {
		debugf("Unlink %q Remove err: %v", path, err)
	}
	f.invalidatePath(path)
	go f.refreshDirNow(f.parentDir(path))
}

func (f *Fs) Mkdir(path string, mode uint32) int {
	debugf("Mkdir %q", path)
	path = f.norm(path)
	if f.usePH {
		go func() {
			if err := f.client.PutPlaceholder(context.Background(), path); err != nil {
				debugf("Mkdir %q placeholder err: %v", path, err)
			}
			go f.refreshDirNow(f.parentDir(path))
		}()
	}
	f.dirs.Invalidate(f.parentDir(path))
	f.metas.Set(path, storage.Meta{IsDir: true, ModTime: time.Now()})
	go f.refreshDirNow(f.parentDir(path))
	return 0
}

func (f *Fs) Rmdir(path string) int {
	path = f.norm(path)
	if entries, ok, stale := f.dirs.Get(path); ok && !stale && len(entries) > 0 {
		return -fuse.ENOTEMPTY
	}
	go func() {
		if err := f.client.RemovePlaceholder(context.Background(), path); err != nil {
			debugf("Rmdir %q RemovePlaceholder err: %v", path, err)
		}
	}()
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
	meta, _ := f.currentMeta(oldpath)
	if metaOld, ok, _ := f.metas.Get(oldpath); ok {
		f.metas.Set(newpath, metaOld)
	}
	f.metas.Invalidate(oldpath)
	key := f.spoolKey(oldpath)
	if f.spool.Exists(key) {
		newKey := f.spoolKey(newpath)
		if err := f.spool.Move(key, newKey); err == nil {
			f.dirs.Invalidate(f.parentDir(oldpath))
			f.dirs.Invalidate(f.parentDir(newpath))
			go f.asyncUpload(newKey, newpath)
			go f.refreshDirNow(f.parentDir(oldpath))
			go f.refreshDirNow(f.parentDir(newpath))
			debugf("Rename %q -> %q via spool move", oldpath, newpath)
			return 0
		}
	}
	f.dirs.Invalidate(f.parentDir(oldpath))
	f.dirs.Invalidate(f.parentDir(newpath))
	go f.doRename(oldpath, newpath, meta.IsDir)
	return 0
}

func (f *Fs) doRename(oldpath, newpath string, isDir bool) {
	f.uploadWG.Add(1)
	defer f.uploadWG.Done()
	if !isDir {
		if err := f.copyFile(oldpath, newpath); err != nil {
			debugf("Rename %q copy err: %v", oldpath, err)
			return
		}
		if err := f.client.Remove(context.Background(), oldpath); err != nil {
			debugf("Rename %q Remove err: %v", oldpath, err)
		}
	} else {
		rels, lerr := f.client.ListRecursive(context.Background(), oldpath)
		if lerr != nil {
			debugf("Rename dir %q ListRecursive err: %v", oldpath, lerr)
			return
		}
		for _, rel := range rels {
			if rel == "" {
				if f.usePH {
					if cerr := f.client.CopyPlaceholder(context.Background(), oldpath, newpath); cerr != nil {
						debugf("Rename dir %q CopyPlaceholder err: %v", oldpath, cerr)
						return
					}
				}
			} else {
				if cerr := f.copyFile(oldpath+"/"+rel, newpath+"/"+rel); cerr != nil {
					debugf("Rename dir %q copy %q err: %v", oldpath, rel, cerr)
					return
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
	go f.refreshDirNow(f.parentDir(oldpath))
	go f.refreshDirNow(f.parentDir(newpath))
}

// copyFile copies src to dst, preferring server-side CopyObject and falling
// back to download+upload for gateways that reject CopyObject (e.g. the
// Hugging Face gateway fails minio-go's copy-source header signature).
func (f *Fs) copyFile(src, dst string) error {
	if err := f.client.Copy(context.Background(), src, dst); err == nil {
		return nil
	}
	rc, size, gerr := f.client.GetFull(context.Background(), src)
	if gerr != nil {
		return gerr
	}
	perr := f.client.Put(context.Background(), dst, rc, size, f.chunkSize)
	rc.Close()
	if perr != nil {
		return perr
	}
	debugf("copyFile %q -> %q via download+upload size=%d", src, dst, size)
	return nil
}

func (f *Fs) Utimens(path string, tmsp []fuse.Timespec) int {
	return 0
}

func (f *Fs) Chmod(path string, mode uint32) int {
	return 0
}
