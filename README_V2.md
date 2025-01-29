## Open Chat V2 ( The Hive Mind )

3rd itteration of open-chat now written in go and federated through libp2p.

### Motivation

There are serverl cool open-weight AI models out there nowadays,
they all require signifcant hardware to run or comprahensive setups that split computations between distributed hardware.

This brings many networking challenges and requires a lot of manual setup most of the time.
The idea is to provide a custom networking solution that allows connection AI components in arbitary setups, over arbitrary 'virtual' network, on any hardware.

### How does the federation work?

Generally to join a open-chat network you need to have that networks 'access token'.
Any node with a networks token may syncronize and discover other network peers with the same network credentials.

For each network each node maintains a network-controller user, that can be authenticated with that networks password.
This user can ONLY syncronize and discover other network peers, but not perform privileged actions on the node.

Each node may additionaly provide an admin user with different credentials, 
this admin user can the be used to perform privileged actions on the node.

e.g.: Say we have to nodes `public-ip-node`, `private-ip-node` and we want then to both join `my-network`

```bash
# on the public-ip-node
backend -dnc my-network:NetworkPassword -rc controller:ControllerPassOfPublicIpNode
backend client login # to retrive current nodes identity, you'll be prompted for controller login credentials
backend client id -base64 # brint the nodes identity in base64 this can be used to discover the network trough another node
# on the private-ip-node
backend -dnc my-network:NetworkPassword -rc controller:ControllerPassOfPrivateIpNode
backend client login # login to the node to add the public-ip-node it's known peers
backend client register -b64 <public-ip-node-identity-base64> -network my-network
# the node with automaticly join the network and sync it's own node to the public-ip-node ...
# after a few seconds the local peer table is syncronized:
backend client nodes -ls
peer_id                                            node_name                                          latest_contact           
-----------------------------------------------------------------------------------------------------------------------------
QmeWkN9dpCQ1w6xK8wLVsH3GrKRtpuzc6bwshQ4rZEm8Le     tims-minipc                                        2025-01-29 15:42:31.230797118 +0100 CET
QmVeVW2uAD7aRVbfPB3JLVshwqQxp26PESAhQjjKnUANFe     QmVeVW2uAD7aRVbfPB3JLVshwqQxp26PESAhQjjKnUANFe     2025-01-27 21:15:19.828121534 +0100 CET
```

Nodes control their own 'node_name' and if a nodes local IP or Port changes it will automatically propagate this change trough the network.

### Persistent Authorized Libp2p tunnels from any node to any other node

Open Chat nodes behave independantly and act autonomously, 
each nodes runs it's own database and persists it's own configuration also across restarts.

With controller credentials of nodes any node may be proxies to any other node:

```bash
# on 'public-ip-node' ( peer_id: QmVeVW2uAD7aRVbfPB3JLVshwqQxp26PESAhQjjKnUANFe )
backend client login
backend client proxy --direction egress --target "QmeWkN9dpCQ1w6xK8wLVsH3GrKRtpuzc6bwshQ4rZEm8Le:1984" --port 8084
# on 'private-ip-node' ( peer_id: QmeWkN9dpCQ1w6xK8wLVsH3GrKRtpuzc6bwshQ4rZEm8Le )
backend client login
backend client proxy --direction ingress --origin "QmVeVW2uAD7aRVbfPB3JLVshwqQxp26PESAhQjjKnUANFe:8084" --port 1984
```

This will proxy all the traffic from `public-ip-node:8084` to the `private-ip-node:1984`.
This works for arbitrary TCP traffic, even database connections.

Proxy configurations are saved to the database and persist / auto-reconnect if a node restarts or goes offline for a while.


### Using the federation network

Any node joined to a network can request any other node in the network.
Most node-apis though require privileged access to perform so always keep the nodes controller credentials near!

E.g.: any node can request another nodes current metrics:

```bash
backend client login
backend client get-metrics -node <node-id> # Will prompt you for the <node-id>s controller credentials
{
    "node_version": "0.0.7",
    # .... Some CPU & Memory usage stats
}
```

Or even get a shell on the federated node:

```bash
backend client login
backend client shell -node <node-id> # Will prompt you for the <node-id>s controller credentials
```

Using the receiving nodes admin credentials another node may even be prompted to update it's own binary based off the binary of the requesting node:

```bash
backend client login
backend client update-node -node <node-id> # Will prompt you for the <node-id>s admin credentials
# the <node-id> node will restart with the updated binary
```