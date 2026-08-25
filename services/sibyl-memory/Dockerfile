FROM python:3.12-slim

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY app.py ./

# PostgreSQL backend — no disk or volume needed.
ENV SIBYL_HOST=0.0.0.0 \
    SIBYL_PORT=8787 \
    PORT=8787

EXPOSE 8787

HEALTHCHECK --interval=15s --timeout=3s --start-period=10s --retries=3 \
  CMD python -c "import urllib.request,sys,os; sys.exit(0 if urllib.request.urlopen('http://127.0.0.1:'+os.environ.get('PORT','8787')+'/health', timeout=2).status==200 else 1)"

CMD ["sh", "-c", "uvicorn app:app --host 0.0.0.0 --port ${PORT:-8787}"]
