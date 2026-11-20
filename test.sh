#!/bin/bash

# Check if the simplemon-agent image is already built
if ! docker images | grep -q 'simplemon-agent'; then
  echo "Building simplemon-agent image..."
  docker build -t simplemon-agent .
else
  echo "simplemon-agent image already exists."
fi

# Base configuration template
CONFIG_TEMPLATE='{
	"serviceUrl":"ws://155.133.27.16:6063",
	"apiKey":"REPLACE_KEY"
}'

# Directory where config files will be stored
CONFIG_DIR="./configs"
mkdir -p $CONFIG_DIR

# Docker image name
IMAGE_NAME="simplemon-agent"

for i in {1..1000}
do
    # Replace apiKey in the template
    CONFIG_FILE="${CONFIG_DIR}/simplemon_${i}.conf"
    echo "${CONFIG_TEMPLATE/REPLACE_KEY/$i}" > $CONFIG_FILE

    # Run docker container with the modified config file
    # docker run -d --name "simplemon_agent_$i" -v "${PWD}/${CONFIG_FILE}:/etc/simplemon.conf" $IMAGE_NAME

    # run simplemon-agent with param $i
    ./bin/simplemon-macos-arm64 $i &

    # Echo the container ID and number
    echo "Container ID: $i"
done
