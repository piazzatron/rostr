# Placeholder until the server stack is chosen: serves "ok" so the Railway deploy and api.getroster.sh can be verified.
FROM busybox:1.36
RUN mkdir /www && echo ok > /www/index.html
CMD ["sh", "-c", "httpd -f -p ${PORT:-8080} -h /www"]
