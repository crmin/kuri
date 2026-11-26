package daemon

import (
	"context"
	"errors"
	"net"

	"golang.org/x/sys/unix"
)

func peer(conn *net.UnixConn) (uid, pid int, err error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, 0, err
	}
	var getErr error
	if err := raw.Control(func(fd uintptr) {
		var cred *unix.Ucred
		cred, getErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		if getErr == nil {
			uid, pid = int(cred.Uid), int(cred.Pid)
		}
	}); err != nil {
		return 0, 0, err
	}
	return uid, pid, getErr
}

func subscribeExit(pid int) (func(context.Context) error, func(), error) {
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		return nil, nil, err
	}
	return func(ctx context.Context) error {
		requests := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		for ctx.Err() == nil {
			n, err := unix.Poll(requests, 100)
			if err != nil && !errors.Is(err, unix.EINTR) {
				return err
			}
			if n > 0 {
				return nil
			}
		}
		return ctx.Err()
	}, func() { _ = unix.Close(fd) }, nil
}
