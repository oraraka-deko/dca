//go:build windows

package main

import (
	"context"
	"fmt"

	"dca/utils"
	"golang.org/x/sys/windows/svc"
)

type mymcpService struct {
	cfg utils.ServerConfig
}

func (m *mymcpService) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (ssec bool, errno uint32) {
	const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}

	serverWrapper := utils.NewMCPServerWrapper(m.cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		err := serverWrapper.StartServer(ctx)
		if err != nil {
			errChan <- err
			return
		}
		// If StartServer starts successfully, block this goroutine until context cancellation
		<-ctx.Done()
		errChan <- nil
	}()

	changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}

	for {
		select {
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				changes <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				_ = serverWrapper.StopServer(ctx)
				cancel()
				return
			}
		case err := <-errChan:
			if err != nil {
				// We can print/log the error, though running as a service stdout goes to null
				fmt.Printf("Service runner error: %v\n", err)
			}
			changes <- svc.Status{State: svc.Stopped}
			return
		}
	}
}

func runAsService(cfg utils.ServerConfig) (bool, error) {
	isSvc, err := svc.IsWindowsService()
	if err != nil {
		return false, err
	}
	if !isSvc {
		return false, nil
	}

	err = svc.Run("mymcp", &mymcpService{cfg: cfg})
	if err != nil {
		return true, err
	}
	return true, nil
}
