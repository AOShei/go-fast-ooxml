#!/usr/bin/env python3
"""Make the password-protected fixtures in testdata/encrypted.

  encrypt.py msoffcrypto <in> <out> <password>   with msoffcrypto-tool, which
      wraps the file as it is (run with the comparison environment's Python)
  encrypt.py libreoffice <in> <out> <password>   with LibreOffice, which loads
      the file and saves it again, encrypted (run with a Python that has
      LibreOffice's "uno" module, usually the system's)

msoffcrypto-tool writes the scheme of Office 2010 and later ("agile", AES-256,
SHA-512). LibreOffice, saving this way, writes the scheme of Office 2007
("standard", AES-128). Files of both schemes written by Microsoft Office are in
the corpus.

The salts are random, so the fixtures are not the same on every run; they are
committed, and listed with their hashes in testdata/encrypted.json.
"""
import os
import resource
import shutil
import signal
import subprocess
import sys
import tempfile
import time

FILTERS = {
    ".docx": "MS Word 2007 XML",
    ".xlsx": "Calc MS Excel 2007 XML",
    ".pptx": "Impress MS PowerPoint 2007 XML",
}


def with_msoffcrypto(src, dst, password):
    import msoffcrypto

    with open(src, "rb") as f:
        office = msoffcrypto.OfficeFile(f)
        with open(dst, "wb") as out:
            office.encrypt(password, out)


def with_libreoffice(src, dst, password):
    import uno
    from com.sun.star.beans import PropertyValue

    def prop(name, value):
        p = PropertyValue()
        p.Name, p.Value = name, value
        return p

    # LibreOffice runs in a process group of its own, with a profile of its
    # own, cannot write a file larger than 64 MB, and is killed when done.
    profile = tempfile.mkdtemp(prefix="gofast-lo-")
    pipe = "gofast%d" % os.getpid()
    proc = subprocess.Popen(
        ["soffice", "-env:UserInstallation=file://" + profile, "--headless", "--norestore",
         "--accept=pipe,name=%s;urp;" % pipe],
        start_new_session=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        preexec_fn=lambda: resource.setrlimit(resource.RLIMIT_FSIZE, (64 << 20, 64 << 20)))
    try:
        local = uno.getComponentContext()
        resolver = local.ServiceManager.createInstanceWithContext("com.sun.star.bridge.UnoUrlResolver", local)
        remote = None
        for _ in range(150):
            try:
                remote = resolver.resolve("uno:pipe,name=%s;urp;StarOffice.ComponentContext" % pipe)
                break
            except Exception:
                time.sleep(0.2)
        if remote is None:
            sys.exit("LibreOffice did not start")
        desktop = remote.ServiceManager.createInstanceWithContext("com.sun.star.frame.Desktop", remote)
        doc = desktop.loadComponentFromURL(uno.systemPathToFileUrl(os.path.abspath(src)), "_blank", 0, (prop("Hidden", True),))
        ext = os.path.splitext(dst)[1].lower()
        doc.storeToURL(uno.systemPathToFileUrl(os.path.abspath(dst)), (prop("FilterName", FILTERS[ext]), prop("Password", password)))
        doc.close(True)
        try:
            desktop.terminate()
        except Exception:
            pass
        try:
            proc.wait(timeout=20)
        except subprocess.TimeoutExpired:
            pass
    finally:
        try:
            os.killpg(proc.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        shutil.rmtree(profile, ignore_errors=True)


def main():
    if len(sys.argv) != 5 or sys.argv[1] not in ("msoffcrypto", "libreoffice"):
        sys.exit(__doc__)
    how, src, dst, password = sys.argv[1:]
    (with_msoffcrypto if how == "msoffcrypto" else with_libreoffice)(src, dst, password)


if __name__ == "__main__":
    main()
