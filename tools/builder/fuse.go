package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"time"
)

func fusefat_mount(imagePath string, mountPoint string, logger *slog.Logger) (func(silent bool), error) {
	logger.Info("Mounting FAT filesystem", slog.String("image", imagePath), slog.String("mount_point", mountPoint))
	err := os.MkdirAll(mountPoint, 0755)
	if err != nil {
		return nil, fmt.Errorf("failed to create mount point: %w", err)
	}

	cmd := fmt.Sprintf("fusefat -o rw+ %s %s", imagePath, mountPoint)
	logger.Info("Executing command", slog.String("cmd", cmd))

	executable := "fusefat"
	_, err = exec.LookPath(executable)
	if err != nil {
		executable = "fusefatfs"
		_, err = exec.LookPath(executable)
		if err != nil {
			return nil, fmt.Errorf("neither 'fusefat' nor 'fusefatfs' commands are available: %w", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel() // Always call cancel to release resources

	output, err := exec.CommandContext(ctx, executable, "-o", "rw+", imagePath, mountPoint).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to mount FAT filesystem: %w, output: %s", err, output)
	}
	return func(silent bool) {
		if DISABLE_UNMOUNTS {
			logger.Info("Skipping unmount due to DISABLE_UNMOUNTS being set", slog.String("mount_point", mountPoint))
			return
		}
		logger.Debug("Unmounting FAT filesystem", slog.String("mount_point", mountPoint))
		err := exec.Command("fusermount", "-u", mountPoint).Run()
		if err != nil && !silent {
			logger.Error("Failed to unmount FAT filesystem", slog.String("mount_point", mountPoint), "error", err)
		}
	}, nil
}

// fuse2fs -o rw,offset=16777216 ./imgs/DietPi_RadxaZERO3-ARMv8-Trixie.img ./test
func fuse2fs_mount(imagePath string, mountPoint string, offset int, logger *slog.Logger) (func(silent bool), error) {
	logger.Info("Mounting EXT filesystem", slog.String("image", imagePath), slog.String("mount_point", mountPoint))
	err := os.MkdirAll(mountPoint, 0755)
	if err != nil {
		return nil, fmt.Errorf("failed to create mount point: %w", err)
	}

	cmd := fmt.Sprintf("fuse2fs -o rw,offset=%d %s %s", offset, imagePath, mountPoint)
	logger.Info("Executing command", slog.String("cmd", cmd))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel() // Always call cancel to release resources

	output, err := exec.CommandContext(ctx, "fuse2fs", "-o", fmt.Sprintf("rw,offset=%d", offset), imagePath, mountPoint).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to mount FAT filesystem: %w, output: %s", err, output)
	}
	return func(silent bool) {
		if DISABLE_UNMOUNTS {
			logger.Info("Skipping unmount due to DISABLE_UNMOUNTS being set", slog.String("mount_point", mountPoint))
			return
		}
		logger.Debug("Unmounting FAT filesystem", slog.String("mount_point", mountPoint))
		err := exec.Command("fusermount", "-u", mountPoint).Run()
		if err != nil && !silent {
			logger.Error("Failed to unmount FAT filesystem", slog.String("mount_point", mountPoint), "error", err)
		}
	}, nil
}
