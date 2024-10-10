package main

import (
	"fmt"
	"math"
	"net/http"
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

	Network []net.IOCountersStat
}

func truncateToDecimals(value float64, precision int) float64 {
	mul := math.Pow(10, float64(precision))

	return math.Trunc(value*mul) / mul
}

func getMetrics() (Metrics, error) {
	var metrics Metrics

	// CPU Usage
	cpuPercent, err := cpu.Percent(0, true)
	if err != nil {
		return metrics, err
	}
	metrics.CPU = cpuPercent

	// range on cpu metrics for value upto two digit

	for i, v := range metrics.CPU {
		metrics.CPU[i] = truncateToDecimals(v, 2)
	}

	// Memory Usage
	memStats, err := mem.VirtualMemory()
	if err != nil {
		return metrics, err
	}
	metrics.Memory.Total = memStats.Total
	metrics.Memory.Free = memStats.Free
	metrics.Memory.Used = memStats.Used

	// Disk Usage
	diskStats, err := disk.Usage("/")
	if err != nil {
		return metrics, err
	}
	metrics.Disk.Total = diskStats.Total
	metrics.Disk.Free = diskStats.Free
	metrics.Disk.Used = diskStats.Used

	// how to check network usage in golang

	// Network Usage
	netStats, err := net.IOCounters(true)

	if err != nil {
		return metrics, err
	}

	metrics.Network = netStats

	return metrics, nil
}

func main() {

	gin.SetMode(gin.ReleaseMode)
	server := gin.Default()

	server.GET("/metrics", func(c *gin.Context) {
		// create a websocket connection
		ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)

		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   err.Error(),
				"message": "Could not open websocket connection",
			})

			return
		}

		conn := ws

		defer func() {
			fmt.Println("Closing connection")
			// conn.Close()
		}()

		go func() {
			for {
				metrics, err := getMetrics()
				if err != nil {
					fmt.Println("socket gives error")
					conn.WriteMessage(websocket.TextMessage, []byte(err.Error()))
					return
				}

				time.Sleep(1 * time.Second)

				err = conn.WriteJSON(metrics)
				if err != nil {
					conn.Close()
					return
				}
			}
		}()
	})

	fmt.Println("Server running on port 8082")
	server.Run(":8082")
}
