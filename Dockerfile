# set base image (host OS)
FROM python:3.13.5-slim-bookworm

# set the working directory in the container
WORKDIR /app

# unbuffered output so that "docker logs" shows everything immediately
ENV PYTHONUNBUFFERED=1

# copy the dependencies file to the working directory
COPY requirements.txt .

# install dependencies
RUN pip install --no-cache-dir --upgrade pip \
    && pip install --no-cache-dir -r requirements.txt

# copy the content of the local src directory to the working directory
COPY . .

# Defaults for container use:
#  - courses are mounted at /app/courses and are the only browsable/served root
#  - progress is stored in /app/data so that course mounts can stay read-only
ENV OFFLINEU_ROOTS=/app/courses \
    OFFLINEU_PROGRESS_DIR=/app/data \
    OFFLINEU_HOST=0.0.0.0 \
    OFFLINEU_PORT=5000

EXPOSE 5000

# add healthcheck using Python standard library
HEALTHCHECK --interval=30s --timeout=10s --start-period=10s --retries=3 \
  CMD python -c "import urllib.request; urllib.request.urlopen('http://localhost:5000/health').read()"

# command to run on container start
CMD [ "python", "/app/offlineu_core.py" ]
