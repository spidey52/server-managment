package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os/exec"
	"sort"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/shirou/gopsutil/net"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

var connections = make(map[*websocket.Conn]bool)
var connLock sync.Mutex
var netLock sync.Mutex

func addConnection(conn *websocket.Conn) {
	connLock.Lock()
	connections[conn] = true
	connLock.Unlock()
}

func removeConnection(conn *websocket.Conn) {
	connLock.Lock()
	delete(connections, conn)
	connLock.Unlock()
}

type NetworkUsage struct {
	Name      string `json:"name"`
	BytesSent uint64 `json:"bytes_sent"`
	BytesRecv uint64 `json:"bytes_recv"`
}

type Metrics struct {
	CPU    []float64 `json:"cpu"`
	Memory struct {
		Total uint64 `json:"total"`
		Free  uint64 `json:"free"`
		Used  uint64 `json:"used"`
	} `json:"memory"`
	Disk struct {
		Total uint64 `json:"total"`
		Free  uint64 `json:"free"`
		Used  uint64 `json:"used"`
	} `json:"disk"`
	Network []NetworkUsage `json:"network"`
	PM2     []PM2Process   `json:"pm2"`
}

type PM2Process struct {
	Name  string `json:"name"`
	PID   int    `json:"pid"`
	PM2ID int    `json:"pm_id"`
	Monit struct {
		Memory int `json:"memory"`
		CPU    int `json:"cpu"`
	} `json:"monit"`
	PM2Env struct {
		Status string `json:"status"`
	} `json:"pm2_env"`
}

func truncateToDecimals(value float64, precision int) float64 {
	mul := math.Pow(10, float64(precision))
	return math.Trunc(value*mul) / mul
}

func getPm2Metrics() ([]PM2Process, error) {
	cmd := exec.Command("pm2", "jlist")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var processes []PM2Process
	err = json.Unmarshal(output, &processes)
	return processes, err
}

func getMetrics(networkMetrics map[string]NetworkUsage) (Metrics, error) {
	var metrics Metrics

	cpuPercent, err := cpu.Percent(0, true)
	if err != nil {
		return metrics, err
	}
	for i, v := range cpuPercent {
		cpuPercent[i] = truncateToDecimals(v, 2)
	}
	metrics.CPU = cpuPercent

	memStats, err := mem.VirtualMemory()
	if err != nil {
		return metrics, err
	}
	metrics.Memory.Total = memStats.Total
	metrics.Memory.Free = memStats.Free
	metrics.Memory.Used = memStats.Used

	diskStats, err := disk.Usage("/")
	if err != nil {
		return metrics, err
	}
	metrics.Disk.Total = diskStats.Total
	metrics.Disk.Free = diskStats.Free
	metrics.Disk.Used = diskStats.Used

	netStats, err := net.IOCounters(true)
	if err != nil {
		return metrics, err
	}

	netLock.Lock()
	for _, netStat := range netStats {
		deltaSent := netStat.BytesSent
		deltaRecv := netStat.BytesRecv
		if prev, ok := networkMetrics[netStat.Name]; ok {
			deltaSent -= prev.BytesSent
			deltaRecv -= prev.BytesRecv
		}
		networkMetrics[netStat.Name] = NetworkUsage{
			Name:      netStat.Name,
			BytesSent: netStat.BytesSent,
			BytesRecv: netStat.BytesRecv,
		}
		metrics.Network = append(metrics.Network, NetworkUsage{
			Name:      netStat.Name,
			BytesSent: deltaSent,
			BytesRecv: deltaRecv,
		})
	}
	sort.Slice(metrics.Network, func(i, j int) bool {
		return metrics.Network[i].Name < metrics.Network[j].Name
	})
	netLock.Unlock()

	pm2Metrics, err := getPm2Metrics()
	if err == nil {
		metrics.PM2 = pm2Metrics
	}

	return metrics, nil
}

func sendMetrics() {
	networkMetrics := make(map[string]NetworkUsage)
	for {
		time.Sleep(1 * time.Second)

		connLock.Lock()
		if len(connections) == 0 {
			connLock.Unlock()
			continue
		}
		connLock.Unlock()

		metrics, err := getMetrics(networkMetrics)
		if err != nil {
			log.Println("Failed to get metrics:", err)
			continue
		}

		connLock.Lock()
		for conn := range connections {
			err := conn.WriteJSON(metrics)
			if err != nil {
				log.Println("Failed to write to websocket:", err)
				conn.Close()
				delete(connections, conn)
			}
		}
		connLock.Unlock()
	}
}

func main() {
	gin.SetMode(gin.ReleaseMode)
	server := gin.Default()

	go sendMetrics()

	server.GET("/metrics", func(c *gin.Context) {
		ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   err.Error(),
				"message": "Could not open websocket connection",
			})
			return
		}

		addConnection(ws)

		go func(conn *websocket.Conn) {
			defer func() {
				conn.Close()
				removeConnection(conn)
			}()
			for {
				if _, _, err := conn.NextReader(); err != nil {
					break
				}
			}
		}(ws)
	})

	fmt.Println("Server running on port 8082")
	server.Run(":8082")
}
