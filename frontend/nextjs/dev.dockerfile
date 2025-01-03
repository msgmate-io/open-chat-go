FROM node:22-alpine AS deps

WORKDIR /frontend
ENV NEXT_PUBLIC_SERVER_URL=http://backend:1984

CMD sh -c "npm install | cat && npm run dev | cat"