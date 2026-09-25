package backupjob

// 远程备份桶的窄边界。
//
// 这里不复用 objectstore.Client：那个绑死了一个桶、一套 presign 用法和 Garage 的
// 部署形态，而远程桶是运维随时能改的任意一家云。Runner 依赖下面这个接口而不是
// minio，测试才能不联网就把上传、校验、过期清理三条路径跑完。

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"easygpa/backend/internal/netguard"
	"easygpa/backend/internal/opsconfig"
)

type RemoteObject struct {
	Key          string
	Size         int64
	LastModified time.Time
}

type RemoteStore interface {
	Put(ctx context.Context, key string, reader io.Reader) (int64, error)
	Stat(ctx context.Context, key string) (int64, error)
	Range(ctx context.Context, key string, offset, length int64) ([]byte, error)
	List(ctx context.Context, prefix string) ([]RemoteObject, error)
	Remove(ctx context.Context, key string) error
}

// remoteFactory 让 Runner 在每次任务开始时按当时的运维配置建连接。配置在库里，
// 运维改完不需要重启 Worker。
type remoteFactory func(opsconfig.BackupRemoteRuntime) (RemoteStore, error)

type bucketStore struct {
	s3     *minio.Client
	bucket string
}

// NewRemoteStore 按运维填的参数建一个 S3 客户端。
func NewRemoteStore(runtime opsconfig.BackupRemoteRuntime) (RemoteStore, error) {
	if strings.TrimSpace(runtime.Endpoint) == "" || strings.TrimSpace(runtime.Bucket) == "" {
		return nil, errors.New("远程备份桶缺少地址或桶名")
	}
	// 腾讯 COS 和阿里 OSS 只认虚拟主机式，R2 和自建 MinIO 只认路径式。
	// BucketLookupAuto 对非 AWS 域名判断不可靠，所以这里按运维选的来，不自动猜。
	lookup := minio.BucketLookupDNS
	if runtime.PathStyle {
		lookup = minio.BucketLookupPath
	}
	client, err := minio.New(runtime.Endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(runtime.AccessKey, runtime.SecretKey, ""),
		Secure:       runtime.UseSSL,
		Region:       runtime.Region,
		BucketLookup: lookup,
		Transport:    remoteTransport(runtime.AllowLoopback),
	})
	if err != nil {
		return nil, err
	}
	return &bucketStore{s3: client, bucket: runtime.Bucket}, nil
}

// Resolve and pin each connection to a checked address, including virtual-host
// bucket names and redirects. Ambient proxies must never receive bucket keys.
func remoteTransport(allowLoopback bool) *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.ResponseHeaderTimeout = 30 * time.Second
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		addresses, err := netguard.BackupAddresses(ctx, host)
		if err != nil {
			return nil, err
		}
		var dialErr error
		for _, candidate := range addresses {
			if netguard.Blocked(candidate.IP) && !(allowLoopback && candidate.IP.IsLoopback()) {
				return nil, errors.New("remote backup endpoint resolved to a blocked address")
			}
		}
		for _, candidate := range addresses {
			connection, err := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.IP.String(), port))
			if err == nil {
				return connection, nil
			}
			dialErr = errors.Join(dialErr, err)
		}
		if dialErr == nil {
			dialErr = errors.New("remote backup endpoint has no public address")
		}
		return nil, dialErr
	}
	return transport
}

// remotePartSize 是分片上传的缓冲大小。minio 的默认值按对象总大小推算，而我们
// 传的是长度未知的流（size = -1），它会退到 16 MiB 一片；显式写死并把并发压到 1，
// 是为了让这台机器的内存占用可预测——备份 Worker 和 PostgreSQL 挤在同一台上。
const remotePartSize = 16 << 20

func (b *bucketStore) Put(ctx context.Context, key string, reader io.Reader) (int64, error) {
	info, err := b.s3.PutObject(ctx, b.bucket, key, reader, -1, minio.PutObjectOptions{
		ContentType: "application/octet-stream",
		PartSize:    remotePartSize,
		NumThreads:  1,
	})
	if err != nil {
		return 0, err
	}
	return info.Size, nil
}

func (b *bucketStore) Stat(ctx context.Context, key string) (int64, error) {
	info, err := b.s3.StatObject(ctx, b.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return 0, err
	}
	return info.Size, nil
}

func (b *bucketStore) Range(ctx context.Context, key string, offset, length int64) ([]byte, error) {
	options := minio.GetObjectOptions{}
	if err := options.SetRange(offset, offset+length-1); err != nil {
		return nil, err
	}
	object, err := b.s3.GetObject(ctx, b.bucket, key, options)
	if err != nil {
		return nil, err
	}
	defer object.Close()
	// 多读一个字节：服务端要是忽略了 Range 头把整个对象吐回来，下面的长度检查
	// 就会发现，而不是拿前 length 个字节当成"范围请求成功"。
	data, err := io.ReadAll(io.LimitReader(object, length+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != length {
		return nil, fmt.Errorf("远程对象 %s 的范围请求返回了 %d 字节，期望 %d", key, len(data), length)
	}
	return data, nil
}

func (b *bucketStore) List(ctx context.Context, prefix string) ([]RemoteObject, error) {
	items := make([]RemoteObject, 0)
	for item := range b.s3.ListObjects(ctx, b.bucket, minio.ListObjectsOptions{Recursive: true, Prefix: prefix}) {
		if item.Err != nil {
			return nil, item.Err
		}
		items = append(items, RemoteObject{Key: item.Key, Size: item.Size, LastModified: item.LastModified})
	}
	return items, nil
}

func (b *bucketStore) Remove(ctx context.Context, key string) error {
	return b.s3.RemoveObject(ctx, b.bucket, key, minio.RemoveObjectOptions{})
}

// RemoteProbe 记录连通性自检走到了哪一步。三个动作分开报，是因为它们的失败
// 原因完全不同：写不进多半是密钥或权限，读不到多半是路径风格选反了（对象进了
// 另一个桶名），删不掉是桶策略只给了写权限——那样过期清理将来会一直失败。
type RemoteProbe struct {
	Wrote   bool `json:"wrote"`
	Read    bool `json:"read"`
	Removed bool `json:"removed"`
}

const probeBody = "easygpa remote backup connectivity probe"

// ProbeRemote 往桶里写一个小对象、读回来、再删掉。运维页的「测试连接」用它，
// 走的是和真备份完全一样的那套客户端参数。
func ProbeRemote(ctx context.Context, store RemoteStore, prefix string) (RemoteProbe, error) {
	key := prefix + ".easygpa-connectivity-" + randomHex(8)
	var probe RemoteProbe
	if _, err := store.Put(ctx, key, strings.NewReader(probeBody)); err != nil {
		return probe, fmt.Errorf("写入测试对象失败：%w", err)
	}
	probe.Wrote = true
	// 写完立刻尽力删掉，后面任何一步失败都不会在桶里留垃圾。
	defer func() {
		if !probe.Removed {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			_ = store.Remove(cleanup, key)
		}
	}()
	data, err := store.Range(ctx, key, 0, int64(len(probeBody)))
	if err != nil {
		return probe, fmt.Errorf("读回测试对象失败：%w", err)
	}
	if string(data) != probeBody {
		return probe, errors.New("读回的测试对象内容与写入的不一致")
	}
	probe.Read = true
	if err := store.Remove(ctx, key); err != nil {
		return probe, fmt.Errorf("删除测试对象失败（过期清理将来也会失败）：%w", err)
	}
	probe.Removed = true
	return probe, nil
}
