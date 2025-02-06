FROM node:22-alpine AS deps

WORKDIR /frontend

CMD sh -c "npm install | cat && npm run dev | cat"