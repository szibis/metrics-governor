#!/bin/bash
set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${GREEN}=== metrics-governor Test Environment ===${NC}"
echo ""

# Change to project root
cd "$(dirname "$0")/.."

# Build and start all services
echo -e "${YELLOW}Building and starting services...${NC}"
docker compose up --build -d

echo ""
echo -e "${GREEN}Services started successfully!${NC}"
echo ""
echo "Available endpoints:"
echo "  - metrics-governor gRPC:    localhost:4317"
echo "  - metrics-governor HTTP:    localhost:4318"
echo "  - metrics-governor stats:   http://localhost:9090/metrics"
echo "  - OTel Collector gRPC:      localhost:14317"
echo "  - OTel Collector Prometheus: http://localhost:8889/metrics"
echo "  - Prometheus UI:            http://localhost:9091"
echo ""
echo -e "${YELLOW}Useful commands:${NC}"
echo "  View metrics-governor stats:"
echo "    curl -s localhost:9090/metrics | grep metrics_governor"
echo ""
echo "  View logs:"
echo "    docker compose logs -f metrics-governor"
echo "    docker compose logs -f metrics-generator"
echo ""
echo "  Check limit violations:"
echo "    curl -s localhost:9090/metrics | grep limit"
echo ""
echo "  Stop all services:"
echo "    docker compose down"
echo ""

# Wait for services to be ready
echo -e "${YELLOW}Waiting for services to be ready...${NC}"
sleep 10

# Show initial stats
echo ""
echo -e "${GREEN}Initial metrics-governor stats:${NC}"
curl -s localhost:9090/metrics 2>/dev/null | grep -E "^metrics_governor" | head -20 || echo "Waiting for metrics..."

echo ""
echo -e "${GREEN}Test environment is running!${NC}"
echo "Press Ctrl+C to stop watching logs, then run 'docker compose down' to stop services."
echo ""

# Follow logs
docker compose logs -f metrics-governor metrics-generator
