FROM python:3.7 AS build

WORKDIR /app
COPY . .

RUN pip3 install -r requirements.txt

# Run as daemon user, use of root is discouraged
USER 2

ENTRYPOINT ["/apps/deploment-reaper.py"]
