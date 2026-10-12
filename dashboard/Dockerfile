FROM nginx:1.27-alpine AS runtime
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY dist /usr/share/nginx/html
EXPOSE 80

HEALTHCHECK --interval=10s --timeout=5s --start-period=10s --retries=6 \
    CMD wget --quiet --spider http://127.0.0.1/ || exit 1
