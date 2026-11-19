import os

from flask import Flask, request, send_file

app = Flask(__name__)
UPLOAD_DIR = "/srv/uploads"


@app.route("/files/<name>")
def download(name):
    path = os.path.join(UPLOAD_DIR, request.args.get("dir", ""), name)
    return send_file(path)


@app.route("/files", methods=["POST"])
def upload():
    f = request.files["file"]
    f.save(os.path.join(UPLOAD_DIR, f.filename))
    return {"ok": True}
