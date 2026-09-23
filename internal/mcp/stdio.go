package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"go.mewis.me/codemcp/internal/idgen"
	"go.mewis.me/codemcp/internal/tools"
)

var ErrStdioMessageTooLarge = errors.New("stdio MCP message exceeds size limit")

type StdioRuntime struct {
	Server *SDKServer
	In     io.ReadCloser
	Out    io.WriteCloser
}

func NewStdioRuntime(toolRuntime *tools.Runtime, in io.ReadCloser, out io.WriteCloser) (*StdioRuntime, error) {
	return NewStdioRuntimeWithWorkspace(toolRuntime, in, out, "")
}

func NewStdioRuntimeWithWorkspace(toolRuntime *tools.Runtime, in io.ReadCloser, out io.WriteCloser, workspaceID string) (*StdioRuntime, error) {
	server, err := NewSDKServerWithSession(toolRuntime, "stdio", idgen.Must("stdio", 8), workspaceID)
	if err != nil {
		return nil, err
	}
	return &StdioRuntime{Server: server, In: in, Out: out}, nil
}

func (r *StdioRuntime) Run(ctx context.Context) error {
	return r.Server.Server.Run(ctx, &sdkmcp.IOTransport{Reader: newLineLimitReadCloser(r.In, MaxRequestBodyBytes), Writer: r.Out})
}

type lineLimitReadCloser struct {
	reader  io.ReadCloser
	max     int64
	current int64
	failed  error
}

func newLineLimitReadCloser(reader io.ReadCloser, max int64) io.ReadCloser {
	return &lineLimitReadCloser{reader: reader, max: max}
}

func (r *lineLimitReadCloser) Read(buffer []byte) (int, error) {
	if r.failed != nil {
		return 0, r.failed
	}
	read, err := r.reader.Read(buffer)
	for index, value := range buffer[:read] {
		if value == '\n' {
			r.current = 0
			continue
		}
		r.current++
		if r.current > r.max {
			r.failed = fmt.Errorf("%w: maximum is %d bytes", ErrStdioMessageTooLarge, r.max)
			return index, r.failed
		}
	}
	return read, err
}

func (r *lineLimitReadCloser) Close() error { return r.reader.Close() }
