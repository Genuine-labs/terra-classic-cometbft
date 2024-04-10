#!/bin/bash

# Check if the required argument is provided
if [ $# -eq 0 ]; then
    echo "Usage: $0 <ip1> <ip2> <ip3> ..."
    exit 1
fi

# Command to run on each IP
BASE_COMMAND="cometbft show_node_id --home ./mytestnet/node"

# Initialize an array to store results
PERSISTENT_PEERS=""

# Iterate through provided IPs
for ((i=1; i<=4; i++)); do
    NODE_IDX=$((i - 1)) # Adjust for zero-based indexing
    
    IP="${!i}"
    echo $IP
    echo "Getting ID of $IP (node $NODE_IDX)..."

    # Run the command on the current IP and capture the result
    ID=$($BASE_COMMAND$NODE_IDX)

    # Store the result in the array
    PERSISTENT_PEERS+="$ID@$IP:26656"

    # Add a comma if not the last IP
    if [ $i -lt $# ]; then
        PERSISTENT_PEERS+=","
    fi
done

echo "$PERSISTENT_PEERS"



cometbft node --home ./mytestnet/node0 --proxy_app=kvstore --p2p.persistent_peers="$PERSISTENT_PEERS"
cometbft node --home ./mytestnet/node1 --proxy_app=kvstore --p2p.persistent_peers="$PERSISTENT_PEERS"
cometbft node --home ./mytestnet/node2 --proxy_app=kvstore --p2p.persistent_peers="$PERSISTENT_PEERS"
cometbft node --home ./mytestnet/node3 --proxy_app=kvstore --p2p.persistent_peers="$PERSISTENT_PEERS"


 ./testnet4node.sh 