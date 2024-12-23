#!/bin/bash

# 1 - start first host
cd ./backend
./backend -p 1984 -pp2p 1985 -dp p1.db
./backend -p 1988 -pp2p 1989 -dp p3.db -bp /ip4/127.0.0.1/tcp/1985/p2p/QmPBv3zTeG9FbGqCGZE5gf6cqjMLx5YCwYVUQ5CxJhupXD

# 2 - start second host
# 
./backend -p 1986 -pp2p 1987 -bp /ip4/127.0.0.1/tcp/1985/p2p/QmTpDQmrrs9pGDzx2i7oJEJ4DKtxbDWnDnhKAwg6VNukKo

./backend -p 1986 -pp2p 1987 -bp /ip4/127.0.0.1/tcp/1985/p2p/QmTpDQmrrs9pGDzx2i7oJEJ4DKtxbDWnDnhKAwg6VNukKo
./backend -p 1986 -pp2p 1987 -bp /ip4/127.0.0.1/tcp/1985/p2p/QmPQhcWcvWEUKqaRXmA5V1xcPg7t7Kvst4m9YsRk1vNgkm

./backend -p 1986 -pp2p 1987 -bp /ip4/127.0.0.1/tcp/1985/p2p/QmTpDQmrrs9pGDzx2i7oJEJ4DKtxbDWnDnhKAwg6VNukKo