package common

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/util/progress/progressui"
	"github.com/opencontainers/go-digest"
	log "github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

// DisplayProgress starts a goroutine to display build progress.
// This encapsulates the common pattern used in both generate and patch commands.
// Uses BuildKit's progressui; debug logging forces plain output so step logs stay readable.
func DisplayProgress(ctx context.Context, eg *errgroup.Group, buildChannel chan *client.SolveStatus, progress progressui.DisplayMode) {
	eg.Go(func() error {
		mode := progress
		if log.GetLevel() >= log.DebugLevel {
			mode = progressui.PlainMode
		}

		display, err := progressui.NewDisplay(os.Stderr, mode)
		if err != nil {
			return err
		}

		_, err = display.UpdateFrom(ctx, buildChannel)
		return err
	})
}

const (
	failureLogMaxBytes = 4096
	failureLogMaxLines = 15
)

// FailureLog records BuildKit step output so a failed step's log can be attached to the build error.
type FailureLog struct {
	mu     sync.Mutex
	output map[digest.Digest][]byte
	failed []digest.Digest
}

// Tee records every status from src and forwards it to dst, closing dst when done.
func (f *FailureLog) Tee(ctx context.Context, src <-chan *client.SolveStatus, dst chan<- *client.SolveStatus) {
	defer close(dst)
	for {
		select {
		case status, ok := <-src:
			if !ok {
				return
			}
			f.record(status)
			select {
			case dst <- status:
			case <-ctx.Done():
			}
		case <-ctx.Done():
			for {
				select {
				case status, ok := <-src:
					if !ok {
						return
					}
					f.record(status)
				default:
					return
				}
			}
		}
	}
}

func (f *FailureLog) record(status *client.SolveStatus) {
	if status == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.output == nil {
		f.output = make(map[digest.Digest][]byte)
	}
	for _, l := range status.Logs {
		out := f.output[l.Vertex]
		out = append(out, l.Data...)
		if len(out) > failureLogMaxBytes {
			out = out[len(out)-failureLogMaxBytes:]
		}
		f.output[l.Vertex] = out
	}
	for _, v := range status.Vertexes {
		if v.Error == "" || strings.Contains(v.Error, context.Canceled.Error()) {
			continue
		}
		f.failed = append(f.failed, v.Digest)
	}
}

// Annotate appends the last output lines of the failed steps to err.
func (f *FailureLog) Annotate(err error) error {
	if err == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	seen := make(map[digest.Digest]struct{})
	var lines []string
	for _, d := range f.failed {
		if _, ok := seen[d]; ok {
			continue
		}
		seen[d] = struct{}{}
		for line := range strings.SplitSeq(string(f.output[d]), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				lines = append(lines, line)
			}
		}
	}
	if len(lines) == 0 {
		return err
	}
	if len(lines) > failureLogMaxLines {
		lines = lines[len(lines)-failureLogMaxLines:]
	}
	return fmt.Errorf("%w\n%s", err, strings.Join(lines, "\n"))
}

// ForwardProgressWithPrefix forwards progress from src to dst, prefixing vertex names with the given prefix.
// This allows multiplexing multiple build progress streams into a single display.
func ForwardProgressWithPrefix(ctx context.Context, src <-chan *client.SolveStatus, dst chan<- *client.SolveStatus, prefix string) {
	namePrefix := "[" + prefix + "] "
	digestPrefix := prefix + ":"
	remappedDigests := make(map[digest.Digest]digest.Digest)

	for {
		select {
		case status, ok := <-src:
			if !ok {
				return
			}
			if status == nil {
				continue
			}

			// Clone and prefix vertex names.
			prefixed := &client.SolveStatus{
				Vertexes: make([]*client.Vertex, len(status.Vertexes)),
				Warnings: status.Warnings,
			}

			for i, v := range status.Vertexes {
				// Clone the vertex and prefix its name.
				cloned := *v
				cloned.Name = namePrefix + v.Name
				// Create a new digest that includes the prefix to avoid collisions.
				cloned.Digest = prefixedDigest(remappedDigests, digestPrefix, v.Digest)
				prefixed.Vertexes[i] = &cloned
			}

			// Also update status and log references to use new digests.
			prefixed.Statuses = make([]*client.VertexStatus, len(status.Statuses))
			for i, s := range status.Statuses {
				cloned := *s
				cloned.Vertex = prefixedDigest(remappedDigests, digestPrefix, s.Vertex)
				prefixed.Statuses[i] = &cloned
			}

			prefixed.Logs = make([]*client.VertexLog, len(status.Logs))
			for i, l := range status.Logs {
				cloned := *l
				cloned.Vertex = prefixedDigest(remappedDigests, digestPrefix, l.Vertex)
				prefixed.Logs[i] = &cloned
			}

			select {
			case dst <- prefixed:
			case <-ctx.Done():
				return
			}

		case <-ctx.Done():
			return
		}
	}
}

func prefixedDigest(remappedDigests map[digest.Digest]digest.Digest, digestPrefix string, original digest.Digest) digest.Digest {
	if remapped, ok := remappedDigests[original]; ok {
		return remapped
	}

	remapped := digest.FromString(digestPrefix + original.String())
	remappedDigests[original] = remapped
	return remapped
}
