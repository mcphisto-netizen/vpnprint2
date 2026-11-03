//go:build windows

package main

import (
	"fmt"
	"log"
	"os"
	"strings"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

type servicioVPNPrint struct {
	nombre string
}

func (s *servicioVPNPrint) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	changes <- svc.Status{State: svc.StartPending}
	changes <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		c := <-r
		switch c.Cmd {
		case svc.Interrogate:
			changes <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			changes <- svc.Status{State: svc.StopPending}
			return false, 0
		}
	}
}

func instalarServicio() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("no se pudo conectar al SCM: %v", err)
	}
	defer m.Disconnect()

	nombre := "VPNPrint"
	if strings.Contains(strings.ToLower(exe), "cliente") {
		nombre = "VPNPrint-Cliente"
	} else if strings.Contains(strings.ToLower(exe), "servidor") {
		nombre = "VPNPrint-Servidor"
	}

	s, err := m.OpenService(nombre)
	if err == nil {
		s.Close()
		return fmt.Errorf("el servicio %s ya existe", nombre)
	}

	s, err = m.CreateService(nombre, exe, mgr.Config{
		DisplayName:  nombre,
		Description:  "VPNPrint - impresion remota por VPN/LAN",
		StartType:    mgr.StartAutomatic,
		ErrorControl: mgr.ErrorNormal,
	}, "--servicio")
	if err != nil {
		return fmt.Errorf("no se pudo crear el servicio: %v", err)
	}
	defer s.Close()

	log.Printf("servicio %s instalado", nombre)
	return nil
}

func desinstalarServicio() error {
	exe, _ := os.Executable()
	nombre := "VPNPrint"
	if strings.Contains(strings.ToLower(exe), "cliente") {
		nombre = "VPNPrint-Cliente"
	} else if strings.Contains(strings.ToLower(exe), "servidor") {
		nombre = "VPNPrint-Servidor"
	}

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("no se pudo conectar al SCM: %v", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(nombre)
	if err != nil {
		return fmt.Errorf("servicio %s no encontrado: %v", nombre, err)
	}
	defer s.Close()

	if err := s.Delete(); err != nil {
		return fmt.Errorf("no se pudo eliminar el servicio: %v", err)
	}
	log.Printf("servicio %s eliminado", nombre)
	return nil
}