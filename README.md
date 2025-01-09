## Open Chat V4 ( Msgmate's Hive Mind )

3rd iteration of open-chat now written in Go and federated through libp2p.

> This software is very early stage & experimental! 
> It deliberately has no licence yet and should in no case be used in production!
> Use at your own risk! Contributions & Comments are welcome though.

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

> Genarally Hive-Mind nodes can be setup without root, some featues like persistence on restart and self-update apis only work with root.

```bash
# on the public-ip-node
backend -dnc my-network:NetworkPassword -rc admin:ControllerPassOfPublicIpNode # alternatively pass a pre-hashed password via -rc admin:hash_<SOME PRE HASHED ADMIN PASSWORD>
backend client login # to retrieve current node's identity, you'll be prompted for controller login credentials
backend client id -base64 # print the node's identity in base64; this can be used to discover the network through another node

# on the private-ip-node
backend -dnc my-network:NetworkPassword -rc admin:ControllerPassOfPrivateIpNode
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

### Persistent proxies across NAT's

The hive mind makes use of libp2p's relay capabilities, and add some logic that lets nodes dynamicly reserver relay slots on public nodes, if necessary. Relay connections are transient and are automaticly replaced with direct (hole-punched) connections if possible.

E.g.: quickly create a ssh connection from `tims-minipc` to `tim-labtop` while both are in different networks:

```bash
# client has this dynamic process build in:
backend client shell --node <tim-labtop-peer-id> --network my-network -nc # remove -nc to directly connect trough a shell
#  under the hood this works by creating 3 temporary proxies and one ssh serv
# on <tims-labtop>:
# 1 - ./backend client proxy --direction egress --target "server_username:SomeRandomPassword" --port 2222 --kind ssh
# 2 - ./backend client proxy --direction ingress --origin "<local_peer_id>:2222" --port 2222
# on <tims-minipc>:
# 3 - ./backend client proxy --direction egress --target "<remote_peer_id>:2222" --port 2222
ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -p 2222 @localhost
```

Conveniently even the proxies for `tims-labtop` can be created from `tims-minipc` via realyed requests trough the hive mind network.
This does require knowlege of `tims-labtop` AND `tims-minipc`'s admin user passwords!

### TLS termination anywhere!

The hive mind has very basic TLS challenge solving and termination build in.
E.g.: on a node with a public IP and the correct DNS entry you can easily request a TLS certificate using the client:

```bash
backend client login
backend client tls --hostname=subdomain.example.com --key-prefix=subdomain_example_com
# automaticly solves the letsencrypt ACME challenge and store the certificates to the database
backend client keys # List the keys and optionally copy them to any other hive mind node
backend client proxy --direction egress --target "<local-peer-id>:8080" --port 443

# Now on any other node e.g.: a node behind NAT load the certificates e.g.: tim-labtop
backend client create-key --key-name subdomain_example_com_cert.pem --key-type cert --key-content ...
backend client create-key --key-name subdomain_example_com_key.pem --key-type key --key-content ...
backend client create-key --key-name subdomain_example_com_issuer.pem --key-type issuer --key-content ...

# Now you can tunnel the tls traffice from `subdomain.example.com` to  tims-labtop and terminate the TLS traffic at the local node!
backend client proxy --direction ingress -tls true -key-prefix p2p_signal_api_dev --origin "<public-ip-node-peer-id>:443" -port 8080
```

TADA you server a TLS secured application from a device behind NAT & you made sure that the traffice is encrypted untill it reaches your private node!

### More uses of the federation network

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