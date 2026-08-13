package protocol

import (
	"errors"
	"fmt"
)

const (
	defaultFrameBytes        = 2 << 20
	defaultDecompressedBytes = 8 << 20
	defaultStringBytes       = 1 << 20
	defaultCollectionItems   = 1 << 20
	defaultNBTBytes          = 4 << 20
	defaultPluginBytes       = 1 << 20
	defaultRecursionDepth    = 512
	defaultQueueItems        = 4096

	hardFrameBytes        = 64 << 20
	hardDecompressedBytes = 256 << 20
	hardStringBytes       = 16 << 20
	hardCollectionItems   = 16 << 20
	hardNBTBytes          = 64 << 20
	hardPluginBytes       = 16 << 20
	hardRecursionDepth    = 2048
	hardQueueItems        = 65536
)

// ErrLimitExceeded reports a value outside its supported range.
var ErrLimitExceeded = errors.New("protocol limit exceeds hard ceiling")

// Limits contains finite codec and stream bounds.
// Construct it with NewLimits. The fields stay private so callers cannot
// accidentally create an unlimited or unvalidated value.
type Limits struct {
	valid             bool
	frameBytes        int
	decompressedBytes int
	stringBytes       int
	collectionItems   int
	nbtBytes          int
	pluginBytes       int
	recursionDepth    int
	queueItems        int
}

// LimitOption changes one limit during construction.
type LimitOption func(*Limits) error

// NewLimits returns finite defaults with the requested overrides.
func NewLimits(options ...LimitOption) (Limits, error) {
	limits := Limits{
		valid:             true,
		frameBytes:        defaultFrameBytes,
		decompressedBytes: defaultDecompressedBytes,
		stringBytes:       defaultStringBytes,
		collectionItems:   defaultCollectionItems,
		nbtBytes:          defaultNBTBytes,
		pluginBytes:       defaultPluginBytes,
		recursionDepth:    defaultRecursionDepth,
		queueItems:        defaultQueueItems,
	}

	for _, option := range options {
		if option == nil {
			return Limits{}, fmt.Errorf("%w: nil option", ErrLimitExceeded)
		}
		if err := option(&limits); err != nil {
			return Limits{}, err
		}
	}

	return limits, nil
}

// Valid reports whether NewLimits constructed the value.
func (l Limits) Valid() bool { return l.valid }

// MaxFrameBytes sets the largest encoded frame.
func MaxFrameBytes(value int) LimitOption {
	return bounded("frame bytes", value, hardFrameBytes, func(limits *Limits) {
		limits.frameBytes = value
	})
}

// MaxDecompressedBytes sets the largest decompressed frame.
func MaxDecompressedBytes(value int) LimitOption {
	return bounded("decompressed bytes", value, hardDecompressedBytes, func(limits *Limits) {
		limits.decompressedBytes = value
	})
}

// MaxStringBytes sets the largest encoded string.
func MaxStringBytes(value int) LimitOption {
	return bounded("string bytes", value, hardStringBytes, func(limits *Limits) {
		limits.stringBytes = value
	})
}

// MaxCollectionItems sets the largest length-prefixed collection.
func MaxCollectionItems(value int) LimitOption {
	return bounded("collection items", value, hardCollectionItems, func(limits *Limits) {
		limits.collectionItems = value
	})
}

// MaxNBTBytes sets the largest encoded NBT value.
func MaxNBTBytes(value int) LimitOption {
	return bounded("NBT bytes", value, hardNBTBytes, func(limits *Limits) {
		limits.nbtBytes = value
	})
}

// MaxPluginBytes sets the largest plugin payload.
func MaxPluginBytes(value int) LimitOption {
	return bounded("plugin bytes", value, hardPluginBytes, func(limits *Limits) {
		limits.pluginBytes = value
	})
}

// MaxRecursionDepth sets the largest nested decode depth.
func MaxRecursionDepth(value int) LimitOption {
	return bounded("recursion depth", value, hardRecursionDepth, func(limits *Limits) {
		limits.recursionDepth = value
	})
}

// MaxQueueItems sets the largest managed stream queue.
func MaxQueueItems(value int) LimitOption {
	return bounded("queue items", value, hardQueueItems, func(limits *Limits) {
		limits.queueItems = value
	})
}

// FrameBytes returns the largest encoded frame.
func (l Limits) FrameBytes() int { return l.frameBytes }

// DecompressedBytes returns the largest decompressed frame.
func (l Limits) DecompressedBytes() int { return l.decompressedBytes }

// StringBytes returns the largest encoded string.
func (l Limits) StringBytes() int { return l.stringBytes }

// CollectionItems returns the largest length-prefixed collection.
func (l Limits) CollectionItems() int { return l.collectionItems }

// NBTBytes returns the largest encoded NBT value.
func (l Limits) NBTBytes() int { return l.nbtBytes }

// PluginBytes returns the largest plugin payload.
func (l Limits) PluginBytes() int { return l.pluginBytes }

// RecursionDepth returns the largest nested decode depth.
func (l Limits) RecursionDepth() int { return l.recursionDepth }

// QueueItems returns the largest managed stream queue.
func (l Limits) QueueItems() int { return l.queueItems }

func bounded(name string, value, ceiling int, apply func(*Limits)) LimitOption {
	return func(limits *Limits) error {
		if value < 1 || value > ceiling {
			return fmt.Errorf(
				"%w: %s is %d, allowed range is 1..%d",
				ErrLimitExceeded,
				name,
				value,
				ceiling,
			)
		}

		apply(limits)

		return nil
	}
}
