package meta

import (
	"fmt"
	"net"
)

type AppInfo struct {
	ID          string
	Name        string
	Host        string
	Environment string
	Version     string
}

// GetOutboundIP returns the non-loopback local IP of the machine.
func GetOutboundIP() (string, error) {
	conn, err := net.Dial("udp", "8.8.8.8:80") // Connect to a public server (doesn't send data)
	if err != nil {
		return "", fmt.Errorf("failed to determine outbound IP: %w", err)
	}
	defer func(conn net.Conn) {
		err := conn.Close()
		if err != nil {
			return
		}
	}(conn)

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String(), nil
}
