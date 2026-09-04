FROM node:22-slim
RUN npm install -g typescript && mkdir -p /runner
COPY run.sh /runner/run.sh
RUN chmod +x /runner/run.sh
