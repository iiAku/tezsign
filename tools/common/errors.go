package common

import "errors"

var (
	ErrFailedToOpenImage   = errors.New("failed to open image")
	ErrFailedToResizeImage = errors.New("failed to resize image")

	ErrFailedToPartitionImage      = errors.New("failed to partition image")
	ErrFailedToOpenPartitionTable  = errors.New("failed to open partition table")
	ErrPartitionTableNotGPT        = errors.New("partition table is not GPT")
	ErrFailedToWritePartitionTable = errors.New("failed to write partition table")
	ErrFailedToFormatPartition     = errors.New("failed to format partition")
	ErrUnsupportedImageFlavor      = errors.New("unsupported image flavor")
	ErrFailedToOpenFilesystem      = errors.New("failed to open filesystem")
	ErrFailedToReadDirectory       = errors.New("failed to read directory")
	ErrUnsupportedPartitionTable   = errors.New("unsupported partition table")
	ErrFailedToConfigureImage      = errors.New("failed to configure image")
	ErrUnexpectedPartitionCount     = errors.New("unexpected partition count")
)
