package daemon

import (
	"context"
	"errors"
	"net"
	"time"

	"golang.org/x/sys/unix"
)

func peer(conn *net.UnixConn) (uid, pid int, err error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, 0, err
	}
	var getErr error
	if err := raw.Control(func(fd uintptr) {
		var cred *unix.Xucred
		cred, getErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if getErr == nil {
			uid = int(cred.Uid)
			pid, getErr = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID)
		}
	}); err != nil {
		return 0, 0, err
	}
	return uid, pid, getErr
}

func subscribeExit(pid int) (func(context.Context) error, func(), error) {
	fd, err := unix.Kqueue()
	if err != nil {
		return nil, nil, err
	}
	unix.CloseOnExec(fd)
	closeFD := func() { _ = unix.Close(fd) }
	change := unix.Kevent_t{Ident: uint64(pid), Filter: unix.EVFILT_PROC,
		Flags: unix.EV_ADD | unix.EV_ONESHOT, Fflags: unix.NOTE_EXIT}
	if _, err := unix.Kevent(fd, []unix.Kevent_t{change}, nil, nil); err != nil {
		closeFD()
		return nil, nil, err
	}
	return func(ctx context.Context) error {
		var events [1]unix.Kevent_t
		for ctx.Err() == nil {
			tick := unix.NsecToTimespec(int64(100 * time.Millisecond))
			n, err := unix.Kevent(fd, nil, events[:], &tick)
			if err != nil && !errors.Is(err, unix.EINTR) {
				return err
			}
			if n > 0 {
				return nil
			}
		}
		return ctx.Err()
	}, closeFD, nil
}
