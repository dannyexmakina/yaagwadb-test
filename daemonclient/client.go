// Package daemonclient reaches the daemon through its gRPC socket, using only the published protocol
// (protocol/yaagwadb.proto), so that the test tools do not depend on the daemon's source.
package daemonclient

import (
	"context"
	"errors"
	"io"

	"yaagwadb/protocol"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	// RootInode is the inode number of the root directory (see docs/en/grpc.md).
	RootInode = 1
	// socketScheme is prefixed to the socket path to name a Unix domain socket as the gRPC target.
	socketScheme = "unix:"
	// writePieceLength is the size of one message of file data sent to Write.
	writePieceLength = 1 << 20
	// readFromStart is the offset of a read from the start of the file.
	readFromStart = 0
)

// Client calls the daemon. Service gives the generated client, for calls this package does not wrap.
type Client struct {
	connection *grpc.ClientConn
	Service    protocol.YaagwaDBClient
}

func Dial(socket string) (*Client, error) {
	connection, err := grpc.NewClient(socketScheme+socket, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	return &Client{connection: connection, Service: protocol.NewYaagwaDBClient(connection)}, nil
}

func (c *Client) Close() error { return c.connection.Close() }

func (c *Client) Status(ctx context.Context) (*protocol.StatusResponse, error) {
	return c.Service.Status(ctx, &protocol.Empty{})
}

func (c *Client) Mkdir(ctx context.Context, parent uint64, name string, mode, uid, gid uint32) (*protocol.Attributes, error) {
	return c.Service.Mkdir(ctx, &protocol.MkdirRequest{ParentInode: parent, Name: name, Mode: mode, Uid: uid, Gid: gid})
}

func (c *Client) Rmdir(ctx context.Context, parent uint64, name string) error {
	_, err := c.Service.Rmdir(ctx, &protocol.RmdirRequest{ParentInode: parent, Name: name})
	return err
}

func (c *Client) Lookup(ctx context.Context, parent uint64, name string) (*protocol.Attributes, error) {
	return c.Service.Lookup(ctx, &protocol.LookupRequest{ParentInode: parent, Name: name})
}

func (c *Client) Unlink(ctx context.Context, parent uint64, name string) error {
	_, err := c.Service.Unlink(ctx, &protocol.UnlinkRequest{ParentInode: parent, Name: name})
	return err
}

// Write sends target and then content as one Write stream.
func (c *Client) Write(ctx context.Context, target *protocol.WriteTarget, content []byte) error {
	stream, err := c.Service.Write(ctx)
	if err != nil {
		return err
	}
	err = stream.Send(&protocol.WriteRequest{Content: &protocol.WriteRequest_Target{Target: target}})
	for start := 0; err == nil && start < len(content); start += writePieceLength {
		piece := content[start:min(start+writePieceLength, len(content))]
		err = stream.Send(&protocol.WriteRequest{Content: &protocol.WriteRequest_Data{Data: piece}})
	}
	// A Send fails with io.EOF when the daemon has already ended the stream; its answer tells why
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	_, err = stream.CloseAndRecv()
	return err
}

// Read reads size bytes of a file from the start. A size of 0 reads to the end.
func (c *Client) Read(ctx context.Context, inode uint64, size int64) ([]byte, error) {
	stream, err := c.Service.Read(ctx, &protocol.ReadRequest{Inode: inode, Offset: readFromStart, Size: size})
	if err != nil {
		return nil, err
	}
	var content []byte
	for {
		response, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return content, nil
		}
		if err != nil {
			return nil, err
		}
		content = append(content, response.Data...)
	}
}

// Names lists the names of the entries of a directory.
func (c *Client) Names(ctx context.Context, inode uint64) ([]string, error) {
	stream, err := c.Service.Readdir(ctx, &protocol.ReaddirRequest{Inode: inode})
	if err != nil {
		return nil, err
	}
	var names []string
	for {
		entry, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return names, nil
		}
		if err != nil {
			return nil, err
		}
		names = append(names, entry.Name)
	}
}
