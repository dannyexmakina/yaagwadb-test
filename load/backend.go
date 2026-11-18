package main

import (
	"context"
	"os"
	"path/filepath"

	"yaagwadb/protocol"
	"yaagwadb-test/daemonclient"
)

const (
	// directoryMode and fileMode are the permissions of the created directory and files.
	directoryMode = 0o755
	fileMode      = 0o644
	// noOwnerID is the uid and gid of the created entries.
	noOwnerID = 0
	// readToEnd reads the whole content.
	readToEnd = 0
	// holdOpenFlags opens a held file for writing through the mount.
	holdOpenFlags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
)

// holdProbe is one byte written to a held file through the mount. A write fails once the daemon has given up on the file.
var holdProbe = []byte{0}

// backend is how the load test reaches the files: gRPC or the mount.
type backend interface {
	write(ctx context.Context, name string, content []byte) error
	read(ctx context.Context, name string) ([]byte, error)
	// hold starts a write and keeps it unfinished until release is called.
	hold(ctx context.Context, name string) (*heldWrite, error)
	remove(ctx context.Context, name string) error
	removeDirectory(ctx context.Context) error
	names(ctx context.Context) ([]string, error)
}

// heldWrite is a write started by hold. probe reports whether the daemon still keeps it.
// release ends it and returns the error the client sees at that point.
type heldWrite struct {
	probe   func() error
	release func() error
}

func newBackend(ctx context.Context, config *settings, client *daemonclient.Client, directoryName string) (backend, error) {
	if config.mode == modeMount {
		directory := filepath.Join(config.mountpoint, directoryName)
		return &mountBackend{directory: directory}, os.Mkdir(directory, directoryMode)
	}
	directory, err := client.Mkdir(ctx, daemonclient.RootInode, directoryName, directoryMode, noOwnerID, noOwnerID)
	if err != nil {
		return nil, err
	}
	return &grpcBackend{client: client, directory: directory.Inode, directoryName: directoryName}, nil
}

type grpcBackend struct {
	client        *daemonclient.Client
	directory     uint64
	directoryName string
}

func (b *grpcBackend) target(name string) *protocol.WriteTarget {
	return &protocol.WriteTarget{ParentInode: b.directory, Name: name, Mode: fileMode, Uid: noOwnerID, Gid: noOwnerID}
}

func (b *grpcBackend) write(ctx context.Context, name string, content []byte) error {
	return b.client.Write(ctx, b.target(name), content)
}

func (b *grpcBackend) read(ctx context.Context, name string) ([]byte, error) {
	inode, err := b.client.Lookup(ctx, b.directory, name)
	if err != nil {
		return nil, err
	}
	return b.client.Read(ctx, inode.Inode, readToEnd)
}

// hold sends only the target. The daemon opens a temporary file and waits for the content.
// release cancels the stream, so nothing is registered.
func (b *grpcBackend) hold(ctx context.Context, name string) (*heldWrite, error) {
	streamContext, cancel := context.WithCancel(ctx)
	stream, err := b.client.Service.Write(streamContext)
	if err != nil {
		cancel()
		return nil, err
	}
	if err := stream.Send(&protocol.WriteRequest{Content: &protocol.WriteRequest_Target{Target: b.target(name)}}); err != nil {
		cancel()
		return nil, err
	}
	// The daemon answers a client stream only when it ends it, so an answer before release means it gave up
	ended := make(chan error, 1)
	go func() {
		ended <- stream.RecvMsg(&protocol.WriteResponse{})
	}()
	probe := func() error {
		select {
		case err := <-ended:
			ended <- err
			return err
		default:
			return nil
		}
	}
	release := func() error {
		cancel()
		return nil
	}
	return &heldWrite{probe: probe, release: release}, nil
}

func (b *grpcBackend) remove(ctx context.Context, name string) error {
	return b.client.Unlink(ctx, b.directory, name)
}

func (b *grpcBackend) removeDirectory(ctx context.Context) error {
	return b.client.Rmdir(ctx, daemonclient.RootInode, b.directoryName)
}

func (b *grpcBackend) names(ctx context.Context) ([]string, error) {
	return b.client.Names(ctx, b.directory)
}

type mountBackend struct {
	directory string
}

func (b *mountBackend) path(name string) string {
	return filepath.Join(b.directory, name)
}

func (b *mountBackend) write(_ context.Context, name string, content []byte) error {
	return os.WriteFile(b.path(name), content, fileMode)
}

func (b *mountBackend) read(_ context.Context, name string) ([]byte, error) {
	return os.ReadFile(b.path(name))
}

// hold creates the file and writes one byte, then keeps it open. release closes it, which registers the file.
// Through the mount, a write the daemon gave up on is often reported only by close.
func (b *mountBackend) hold(_ context.Context, name string) (*heldWrite, error) {
	file, err := os.OpenFile(b.path(name), holdOpenFlags, fileMode)
	if err != nil {
		return nil, err
	}
	probe := func() error {
		_, err := file.Write(holdProbe)
		return err
	}
	if err := probe(); err != nil {
		file.Close()
		return nil, err
	}
	return &heldWrite{probe: probe, release: file.Close}, nil
}

func (b *mountBackend) remove(_ context.Context, name string) error {
	return os.Remove(b.path(name))
}

func (b *mountBackend) removeDirectory(_ context.Context) error {
	return os.Remove(b.directory)
}

func (b *mountBackend) names(_ context.Context) ([]string, error) {
	entries, err := os.ReadDir(b.directory)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names, nil
}
