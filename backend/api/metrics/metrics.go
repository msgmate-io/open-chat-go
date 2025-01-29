package metrics

import (
	"backend/server/util"
	"encoding/json"
	"log"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
)

type Metrics struct {
	NodeVersion string       `json:"node_version"`
	CPUInfo     CPUInfo      `json:"cpu_info"`
	MemoryInfo  MemoryInfo   `json:"memory_info"`
	VolumesInfo []VolumeInfo `json:"volumes_info"`
}

type CPUInfo struct {
	TotalCores int                `json:"total_cores"`
	Usage      map[string]float64 `json:"usage"`
}

type MemoryInfo struct {
	TotalGB     float64 `json:"total_gb"`
	UsedPercent float64 `json:"used_percent"`
}

type VolumeInfo struct {
	MountPoint string `json:"mount_point"`
	Usage      string `json:"usage"`
}

func getCPUInfo() (CPUInfo, error) {
	var cpuInfo CPUInfo
	// Get total CPU cores
	out, err := exec.Command("nproc").Output()
	if err != nil {
		log.Printf("Error executing nproc: %v", err)
		return cpuInfo, err
	}
	totalCores, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		log.Printf("Error parsing total cores: %v", err)
		return cpuInfo, err
	}
	cpuInfo.TotalCores = totalCores

	// Get CPU usage per core
	out, err = exec.Command("mpstat", "-P", "ALL", "1", "1").Output()
	if err != nil {
		log.Printf("Error executing mpstat: %v", err)
		return cpuInfo, err
	}

	cpuInfo.Usage = make(map[string]float64)
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		// Check if the line is a data line by ensuring it has more than 10 fields and the second field is a number
		if len(fields) > 10 && fields[0] == "Average:" && fields[1] != "CPU" {
			cpuID := fields[1]
			idle, err := strconv.ParseFloat(fields[11], 64)
			if err != nil {
				log.Printf("Error parsing idle percentage for CPU %s: %v", cpuID, err)
				return cpuInfo, err
			}
			// Calculate the usage as 100% minus the idle percentage
			cpuInfo.Usage[cpuID] = 100.0 - idle
		}
	}

	return cpuInfo, nil
}

func getMemoryInfo() (MemoryInfo, error) {
	var memInfo MemoryInfo
	out, err := exec.Command("free", "-m").Output()
	if err != nil {
		return memInfo, err
	}

	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "Mem:") {
			fields := strings.Fields(line)
			if len(fields) > 2 {
				totalMB, err := strconv.ParseFloat(fields[1], 64)
				if err != nil {
					return memInfo, err
				}
				usedMB, err := strconv.ParseFloat(fields[2], 64)
				if err != nil {
					return memInfo, err
				}
				memInfo.TotalGB = totalMB / 1024
				memInfo.UsedPercent = (usedMB / totalMB) * 100
			}
		}
	}

	return memInfo, nil
}

func getVolumesInfo() ([]VolumeInfo, error) {
	var volumesInfo []VolumeInfo
	out, err := exec.Command("df", "-h", "--output=target,pcent").Output()
	if err != nil {
		return volumesInfo, err
	}

	lines := strings.Split(string(out), "\n")
	for _, line := range lines[1:] { // Skip header
		fields := strings.Fields(line)
		if len(fields) == 2 {
			volumesInfo = append(volumesInfo, VolumeInfo{
				MountPoint: fields[0],
				Usage:      fields[1],
			})
		}
	}

	return volumesInfo, nil
}

func (h *MetricsHandler) Metrics(w http.ResponseWriter, r *http.Request) {
	_, user, err := util.GetDBAndUser(r)

	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return
	}

	cpuInfo, err := getCPUInfo()
	if err != nil {
		log.Printf("Error getting CPU info: %v", err)
	}

	memInfo, err := getMemoryInfo()
	if err != nil {
		log.Printf("Error getting memory info: %v", err)
	}

	volumesInfo, err := getVolumesInfo()
	if err != nil {
		log.Printf("Error getting volumes info: %v", err)
	}

	metrics := Metrics{
		NodeVersion: VERSION,
		CPUInfo:     cpuInfo,
		MemoryInfo:  memInfo,
		VolumesInfo: volumesInfo,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(metrics)
}
