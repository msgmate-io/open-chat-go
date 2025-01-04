## Open Chat V2 ( The Hive Mind )

3rd iteration of open-chat now written in Go and federated through libp2p.

### Motivation

There are several cool open-weight AI models out there nowadays.  
They all require significant hardware to run, or comprehensive setups that split computations among distributed hardware.

This brings many networking challenges and often requires a lot of manual configuration.  
The idea is to provide a custom networking solution that allows connecting AI components in arbitrary setups, over any 'virtual' network, on any hardware.

### How does the federation work?

Generally, to join an open-chat network you need that network's 'access token'.  
Any node with the network's token may synchronize and discover other network peers with the same network credentials.

For each network, each node maintains a network-controller user that can be authenticated with that network's password.  
This user can ONLY synchronize and discover other network peers, but cannot perform privileged actions on the node.

Each node may additionally provide an admin user with different credentials, which can be used to perform privileged actions on the node.

**Example:** Suppose we have two nodes, `public-ip-node` and `private-ip-node`, and we want them both to join `my-network`:

```bash
# on the public-ip-node
backend -dnc my-network:NetworkPassword -rc controller:ControllerPassOfPublicIpNode
backend client login # to retrieve current node's identity, you'll be prompted for controller login credentials
backend client id -base64 # print the node's identity in base64; this can be used to discover the network through another node

# on the private-ip-node
backend -dnc my-network:NetworkPassword -rc controller:ControllerPassOfPrivateIpNode
backend client login # login to the node to add the public-ip-node as a known peer
backend client register -b64 <public-ip-node-identity-base64> -network my-network
# the node will automatically join the network and synchronize with the public-ip-node ...

# after a few seconds, the local peer table is synchronized:
backend client nodes -ls
peer_id                                            node_name                                          latest_contact           
-----------------------------------------------------------------------------------------------------------------------------
QmeWkN9dpCQ1w6xK8wLVsH3GrKRtpuzc6bwshQ4rZEm8Le     tims-minipc                                        2025-01-29 15:42:31.230797118 +0100 CET
QmVeVW2uAD7aRVbfPB3JLVshwqQxp26PESAhQjjKnUANFe     QmVeVW2uAD7aRVbfPB3JLVshwqQxp26PESAhQjjKnUANFe     2025-01-27 21:15:19.828121534 +0100 CET
```

Nodes control their own `node_name`, and if a node’s local IP or port changes, it will automatically propagate this update through the network.

### Persistent Authorized Libp2p tunnels from any node to any other node

Open Chat nodes behave independently and act autonomously.  
Each node runs its own database and persists its own configuration, even across restarts.

With the controller credentials of a node, any node may proxy to any other node:

```bash
# on 'public-ip-node' ( peer_id: QmVeVW2uAD7aRVbfPB3JLVshwqQxp26PESAhQjjKnUANFe )
backend client login
backend client proxy --direction egress --target "QmeWkN9dpCQ1w6xK8wLVsH3GrKRtpuzc6bwshQ4rZEm8Le:1984" --port 8084

# on 'private-ip-node' ( peer_id: QmeWkN9dpCQ1w6xK8wLVsH3GrKRtpuzc6bwshQ4rZEm8Le )
backend client login
backend client proxy --direction ingress --origin "QmVeVW2uAD7aRVbfPB3JLVshwqQxp26PESAhQjjKnUANFe:8084" --port 1984
```

This will proxy all traffic from `public-ip-node:8084` to `private-ip-node:1984`.  
It works for arbitrary TCP traffic, including database connections.

Proxy configurations are saved to the database and persist / auto-reconnect if a node restarts or goes offline for a while.

### Using the federation network

Any node joined to a network can request information from any other node in the network.  
Most node-APIs, however, require privileged access to perform actions, so always keep the node’s controller credentials handy!

**Example:** Any node can request another node’s current metrics:

```bash
backend client login
backend client get-metrics -node <node-id> # Will prompt you for <node-id>'s controller credentials
{
    "node_version": "0.0.7",
    # .... Some CPU & Memory usage stats
}
```

You can even get a shell on the federated node:

```bash
backend client login
backend client shell -node <node-id> # Will prompt you for <node-id>'s controller credentials
```

Using the receiving node's admin credentials, another node can be prompted to update its own binary (based on the requester's binary):

```bash
backend client login
backend client update-node -node <node-id> # Will prompt you for <node-id>'s admin credentials
# the <node-id> node will restart with the updated binary
```