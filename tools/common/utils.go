package common

import (
	"errors"

	"github.com/diskfs/go-diskfs/disk"
	"github.com/diskfs/go-diskfs/partition/gpt"
	"github.com/diskfs/go-diskfs/partition/mbr"
	"github.com/diskfs/go-diskfs/partition/part"
	"github.com/tez-capital/tezsign/tools/constants"
)

func GetTezsignPartitions(img *disk.Disk) (boot, rootfs, app, data part.Partition, err error) {
	table, err := img.GetPartitionTable()
	if err != nil {
		return nil, nil, nil, nil, errors.Join(ErrFailedToOpenPartitionTable, err)
	}

	var bootPartition part.Partition
	var rootfsPartition part.Partition
	var appPartition part.Partition
	var dataPartition part.Partition

	switch table := table.(type) {
	case *gpt.Table:
		gptTable := table
		if len(gptTable.Partitions) < 3 {
			return nil, nil, nil, nil, errors.Join(ErrFailedToConfigureImage, ErrUnexpectedPartitionCount)
		}
		for _, partition := range gptTable.Partitions {
			switch partition.Name {
			case "boot", "bootfs":
				bootPartition = partition
			case "root", "rootfs":
				rootfsPartition = partition
			case constants.AppPartitionLabel:
				appPartition = partition
			case constants.DataPartitionLabel:
				dataPartition = partition
			}
		}
	case *mbr.Table:
		mbrTable := table
		if len(mbrTable.Partitions) != 4 {
			return nil, nil, nil, nil, errors.Join(ErrFailedToConfigureImage, ErrUnexpectedPartitionCount)
		}

		bootPartition = mbrTable.Partitions[0]
		rootfsPartition = mbrTable.Partitions[1]
		appPartition = mbrTable.Partitions[2]
		dataPartition = mbrTable.Partitions[3]
	default:
		return nil, nil, nil, nil, errors.Join(ErrFailedToPartitionImage, ErrPartitionTableNotGPT)
	}

	if rootfsPartition == nil || appPartition == nil || dataPartition == nil {
		return nil, nil, nil, nil, errors.Join(ErrFailedToConfigureImage, ErrUnexpectedPartitionCount)
	}
	return bootPartition, rootfsPartition, appPartition, dataPartition, nil
}
