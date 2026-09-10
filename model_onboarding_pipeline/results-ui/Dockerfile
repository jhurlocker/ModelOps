# Use Red Hat's official Python 3.11 Universal Base Image (ubi9)
# This is built to run as non-root and is the standard for OpenShift
FROM registry.access.redhat.com/ubi9/python-311

WORKDIR /opt/app-root/src

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY app.py .

EXPOSE 8080

CMD ["gunicorn", "--bind", "0.0.0.0:8080", "--workers", "2", "--timeout", "60", "app:app"]
