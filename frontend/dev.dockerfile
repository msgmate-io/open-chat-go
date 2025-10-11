FROM node:22-alpine AS deps

WORKDIR /frontend

CMD sh -c "npm run dev | cat"