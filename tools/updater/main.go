package main

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"

	"github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/backend/file"
	"github.com/diskfs/go-diskfs/disk"
	"github.com/diskfs/go-diskfs/partition/part"
	"github.com/samber/lo"
	"github.com/tez-capital/tezsign/tools/common"
)

type UpdateKind string

const (
	UpdateKindFull    UpdateKind = "full"
	UpdateKindAppOnly UpdateKind = "app"
)

func validateTezsignImage(disk *disk.Disk, appPartition part.Partition) (bool, error) {
	indexOfAppPartition := lo.IndexOf(disk.Table.GetPartitions(), appPartition)
	if indexOfAppPartition == -1 {
		return false, errors.New("app partition not found")
	}

	fs, err := disk.GetFilesystem(indexOfAppPartition + 1)
	if err != nil {
		return false, errors.New("failed to get filesystem")
	}

	if _, err = fs.OpenFile("/tezsign", os.O_RDONLY); err != nil {
		return false, errors.New("failed to verify that destination device is a TezSign")
	}
	return true, nil
}

func loadImageForUpdate(path string, logger *slog.Logger) (*disk.Disk, part.Partition, part.Partition, part.Partition, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to open device %s: %w", path, err)
	}

	disk, err := diskfs.OpenBackend(file.New(f, false), diskfs.WithOpenMode(diskfs.ReadWriteExclusive), diskfs.WithSectorSize(diskfs.SectorSizeDefault))
	if err != nil {
		return nil, nil, nil, nil, errors.New("failed to open disk backend")
	}

	destinationBootPartition, destinationRootfsPartition, destinationAppPartition, _, err := common.GetTezsignPartitions(disk)
	if err != nil {
		return nil, nil, nil, nil, errors.New("failed to read partitions from the destination device")
	}

	isTezsign, err := validateTezsignImage(disk, destinationAppPartition)
	if err != nil || !isTezsign {
		return nil, nil, nil, nil, errors.New("the destination device is not a valid TezSign image")
	}

	return disk, destinationBootPartition, destinationRootfsPartition, destinationAppPartition, nil
}

func copyPartitionData(srcDisk *disk.Disk, srcPartition part.Partition, dstDisk *disk.Disk, dstPartition part.Partition, logger *slog.Logger) error {
	pr, pw := io.Pipe()
	writableDst, err := dstDisk.Backend.Writable()
	if err != nil {
		return errors.New("failed to get writable backend for destination disk")
	}

	progressLogger := NewProgressLogger(pw, logger)

	var wg sync.WaitGroup
	var readErr, writeErr error
	var readBytes int64

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer pw.Close() // Close the writer side of the pipe when done

		// ReadContents(backend, out io.Writer) streams data FROM the partition TO the provided writer (pw).
		readBytes, readErr = srcPartition.ReadContents(srcDisk.Backend, progressLogger)
		if readErr != nil {
			logger.Error("Failed to read contents from source partition", "error", readErr)
			return
		}
	}()

	writtenBytes, writeErr := dstPartition.WriteContents(writableDst, pr)
	if writeErr != nil {
		logger.Error("Failed to write contents to destination partition", "error", writeErr)
	}
	pr.Close()
	wg.Wait()

	if readErr != nil {
		return errors.New("error occurred while reading from source partition: " + readErr.Error())
	}
	if writeErr != nil {
		return errors.New("error occurred while writing to destination partition: " + writeErr.Error())
	}
	if uint64(readBytes) != writtenBytes {
		return errors.New("mismatch in bytes read and written")
	}
	return nil
}

func main() {
	if len(os.Args) < 3 {
		slog.Error("Usage: tezsign_updater <source_img> <destination_device>")
		os.Exit(1)
	}

	source := os.Args[1]
	destination := os.Args[2]

	kind := UpdateKindFull
	if len(os.Args) >= 4 {
		kind = UpdateKind(os.Args[3])
		switch kind {
		case UpdateKindFull, UpdateKindAppOnly:
			// valid kind
		default:
			slog.Error("Invalid update kind. Valid options are: full, app")
			os.Exit(1)
		}
	}

	slog.Info("Starting TezSign updater", "source", source, "destination", destination)

	// load source image for update
	sourceImg, sourceBootPartition, sourceRootfsPartition, sourceAppPartition, err := loadImageForUpdate(source, slog.Default())
	if err != nil {
		slog.Error("Failed to load source image for update", "error", err.Error())
		os.Exit(1)
	}
	defer sourceImg.Close()

	// Load the image for update
	dstImg, destinationBootPartition, destinationRootfsPartition, destinationAppPartition, err := loadImageForUpdate(destination, slog.Default())
	if err != nil {
		slog.Error("Failed to load image for update", "error", err.Error())
		os.Exit(1)
	}

	if kind == UpdateKindFull {
		if (sourceBootPartition == nil || destinationBootPartition == nil) && (sourceBootPartition != destinationBootPartition) {
			slog.Error("Boot partition missing in source image or destination device, cannot proceed with full update")
			os.Exit(1)
		}
		if sourceBootPartition != nil && sourceBootPartition.GetSize() != destinationBootPartition.GetSize() {
			slog.Error("Boot partition size mismatch between source image and destination device, cannot proceed with update")
			os.Exit(1)
		}

		if sourceRootfsPartition.GetSize() != destinationRootfsPartition.GetSize() {
			slog.Error("Rootfs partition size mismatch between source image and destination device, cannot proceed with update")
			os.Exit(1)
		}

		if sourceAppPartition.GetSize() != destinationAppPartition.GetSize() {
			slog.Error("App partition size mismatch between source image and destination device, cannot proceed with update")
			os.Exit(1)
		}

		if sourceBootPartition != nil {
			slog.Info("Updating boot partition...")
			if err = copyPartitionData(sourceImg, sourceBootPartition, dstImg, destinationBootPartition, slog.Default()); err != nil {
				slog.Error("Failed to update boot partition", "error", err.Error())
				os.Exit(1)
			}
		}

		slog.Info("Updating rootfs partition...")
		if err = copyPartitionData(sourceImg, sourceRootfsPartition, dstImg, destinationRootfsPartition, slog.Default()); err != nil {
			slog.Error("Failed to update rootfs partition", "error", err.Error())
			os.Exit(1)
		}

		slog.Info("Updating app partition...")
		if err = copyPartitionData(sourceImg, sourceAppPartition, dstImg, destinationAppPartition, slog.Default()); err != nil {
			slog.Error("Failed to update app partition", "error", err.Error())
			os.Exit(1)
		}

	}

	if kind == UpdateKindAppOnly {
		// TODO: directly inject tezsign gadget binary
		slog.Info("Updating app partition...")
		if err = copyPartitionData(sourceImg, sourceAppPartition, dstImg, destinationAppPartition, slog.Default()); err != nil {
			slog.Error("Failed to update app partition", "error", err.Error())
			os.Exit(1)
		}
	}
	dstImg.Close()

	slog.Info("Update completed successfully.")
}
