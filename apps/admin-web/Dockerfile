FROM node:20-alpine AS build
WORKDIR /app
COPY apps/admin-web/package.json ./package.json
RUN npm install
COPY apps/admin-web/ ./
RUN npm run build

FROM nginx:1.27-alpine
COPY apps/admin-web/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/dist /usr/share/nginx/html

