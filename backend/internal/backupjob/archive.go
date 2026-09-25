package backupjob

// 把一个已经完成的本地备份目录打成 tar → gzip → age 的一条流。
//
// 全程流式：磁盘上不会出现第二份备份。这台机器的备份卷只按一份的量算过，
// 先落一个 .tar.gz 再上传会在最不该失败的时候把盘塞满。
//
// 加密用 age 而不是自己拿 AEAD 分帧：一个几个 G 的流必须切块加密，而分块方案
// 的坑（帧序号要绑进 AAD、末帧要标记、否则被静默截断也验得过）全在细节里。
// age 的 STREAM 实现是审计过的，我们只写调用。
//
// 用的是公钥加密：生产机上只有 recipient（age1...），私钥离线保管。这台机器被
// 拿下，攻击者能写新备份，但读不了任何一份历史备份。

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"
)

// sampleBytes 是回读校验抽查的首尾块大小。整包重新下载一遍才是完整校验，但那是
// 每天几个 G 的公网流量；抽首尾两块能抓住截断、传错对象和两端损坏，代价是 2 MiB。
const sampleBytes = 1 << 20

// writeEncryptedArchive 把 dir 下的全部内容以 tar.gz 打包、用 recipient 加密后
// 写进 dst。返回时流已经收尾（age 的 WriteCloser 必须 Close 才会写出最后一帧，
// 漏掉 Close 得到的是一个能上传成功、但解不开的文件）。
func writeEncryptedArchive(ctx context.Context, dst io.Writer, dir string, recipient age.Recipient) error {
	encryptor, err := age.Encrypt(dst, recipient)
	if err != nil {
		return fmt.Errorf("初始化归档加密：%w", err)
	}
	compressor := gzip.NewWriter(encryptor)
	archive := tar.NewWriter(compressor)

	if err := addDirectoryToArchive(ctx, archive, dir); err != nil {
		// 出错时也要把三层关掉，否则 io.Pipe 的另一端会一直阻塞。
		return errors.Join(err, archive.Close(), compressor.Close(), encryptor.Close())
	}
	// 关闭顺序必须由内向外：tar 收尾块 → gzip 收尾 → age 末帧。
	if err := archive.Close(); err != nil {
		return errors.Join(fmt.Errorf("收尾 tar：%w", err), compressor.Close(), encryptor.Close())
	}
	if err := compressor.Close(); err != nil {
		return errors.Join(fmt.Errorf("收尾 gzip：%w", err), encryptor.Close())
	}
	if err := encryptor.Close(); err != nil {
		return fmt.Errorf("收尾加密流：%w", err)
	}
	return nil
}

func addDirectoryToArchive(ctx context.Context, archive *tar.Writer, dir string) error {
	return filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		// 备份目录里只应该有普通文件和目录。符号链接不跟随也不打包——跟随会把
		// 目录外的东西吸进备份，原样存则在恢复时留下一个指向别处的悬空链接。
		if !info.Mode().IsRegular() && !info.IsDir() {
			return fmt.Errorf("备份目录里存在非普通文件：%s", relative)
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(relative)
		// 归档里不保留 uid/gid/uname：恢复端的账号和这里不是一套，留着只会在
		// 解包时报无法 chown。
		header.Uid, header.Gid = 0, 0
		header.Uname, header.Gname = "", ""
		if info.IsDir() {
			header.Name += "/"
			return archive.WriteHeader(header)
		}
		if err := archive.WriteHeader(header); err != nil {
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		written, copyErr := io.Copy(archive, file)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil {
			return errors.Join(copyErr, closeErr)
		}
		if written != info.Size() {
			return fmt.Errorf("备份文件 %s 在打包期间大小发生变化", relative)
		}
		return nil
	})
}

// headBuffer 留下最先写进来的 size 个字节。
type headBuffer struct {
	data []byte
	size int
}

func newHeadBuffer(size int) *headBuffer { return &headBuffer{size: size} }

func (h *headBuffer) Write(p []byte) (int, error) {
	if room := h.size - len(h.data); room > 0 {
		h.data = append(h.data, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

func (h *headBuffer) Bytes() []byte { return h.data }

// tailBuffer 留下最后写进来的 size 个字节。
//
// 用环形缓冲而不是"追加后裁掉开头"：后者每次 Write 都要搬一遍 1 MiB，
// 几个 G 的归档按 64 KiB 一块写下来，光搬内存就是几十个 G。
type tailBuffer struct {
	data    []byte
	written int64
	pos     int
}

func newTailBuffer(size int) *tailBuffer { return &tailBuffer{data: make([]byte, size)} }

func (t *tailBuffer) Write(p []byte) (int, error) {
	n := len(p)
	t.written += int64(n)
	size := len(t.data)
	if n >= size {
		copy(t.data, p[n-size:])
		t.pos = 0
		return n, nil
	}
	first := copy(t.data[t.pos:], p)
	if first < n {
		copy(t.data, p[first:])
	}
	t.pos = (t.pos + n) % size
	return n, nil
}

// Bytes 按写入顺序返回最后那段字节。
func (t *tailBuffer) Bytes() []byte {
	size := len(t.data)
	if t.written < int64(size) {
		return append([]byte(nil), t.data[:t.written]...)
	}
	out := make([]byte, 0, size)
	out = append(out, t.data[t.pos:]...)
	out = append(out, t.data[:t.pos]...)
	return out
}

// parseRecipient 解析部署给的 age 公钥。
func parseRecipient(value string) (age.Recipient, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, errors.New("未配置 BACKUP_REMOTE_RECIPIENT，备份不能明文推到第三方云桶")
	}
	if strings.HasPrefix(value, "AGE-SECRET-KEY-") {
		return nil, errors.New("BACKUP_REMOTE_RECIPIENT 填成了私钥。这里只放公钥（age1 开头），私钥必须离线保管")
	}
	recipient, err := age.ParseX25519Recipient(value)
	if err != nil {
		return nil, fmt.Errorf("BACKUP_REMOTE_RECIPIENT 不是有效的 age 公钥：%w", err)
	}
	return recipient, nil
}
