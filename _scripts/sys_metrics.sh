#!/usr/bin/env bash

##
# 1) CPU INFO
##

# Total CPU cores
cpucount=$(nproc)

# CPU usage per core (sampled ~1 second)
mpstat_out=$(mpstat -P ALL 1 1)

echo "===== CPU INFO ====="
echo "Total CPU Cores: $cpucount"
echo "CPU usage per core:"
echo "$mpstat_out" | awk '
  # Example lines match: "Average:     0    1.00    0.00    ...    93.00"
  # We want CPU ID ($2) and %idle (last column).
  /^Average: +[0-9]/ {
    cpu = $2
    idle = $NF      # last field is %idle in newer mpstat versions
    usage = 100 - idle
    printf("  Core %s usage: %.1f%%\n", cpu, usage)
  }
'

##
# 2) MEMORY INFO
##

# Use 'free -m' to get total and used memory in MB
# and then compute usage % to avoid issues with parsing MemAvailable on older kernels.
echo
echo "===== MEMORY INFO ====="
mem_line=$(free -m | awk '/Mem:/ {print $2, $3}')
mem_total_mb=$(echo "$mem_line" | awk '{print $1}')
mem_used_mb=$(echo "$mem_line" | awk '{print $2}')

mem_used_percent=$(awk -v used="$mem_used_mb" -v total="$mem_total_mb" \
  'BEGIN {printf "%.1f", (used / total) * 100}')

# Convert total MB to GB
mem_total_gb=$(awk -v val="$mem_total_mb" 'BEGIN {printf "%.2f", val / 1024}')

echo "Total Memory: $mem_total_gb GB"
echo "Memory Usage: $mem_used_percent%"

##
# 3) VOLUMES USAGE
##

echo
echo "===== VOLUMES USAGE ====="
# Print each mountpoint and fullness percentage
# Skip the first line (header) with tail -n +2
df -h --output=target,pcent | tail -n +2 | while read -r line; do
  mountpoint=$(echo "$line" | awk '{print $1}')
  usage=$(echo "$line" | awk '{print $2}')
  echo "  $mountpoint: $usage"
done