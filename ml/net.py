"""Acesso HTTP com a biblioteca padrão, usado pelos scripts de ml/.

O Python instalado pelo python.org no macOS vem sem certificados raiz, e o
urllib falha com CERTIFICATE_VERIFY_FAILED. Aqui o contexto TLS procura um
arquivo de certificados conhecido (certifi, se instalado, ou o do sistema).
A verificação nunca é desligada.
"""
import json
import os
import ssl
import urllib.request

AGENTE = {"User-Agent": "ml-dataset-builder/1.0"}
_CANDIDATOS = (
    "/etc/ssl/cert.pem",                    # macOS, BSD
    "/etc/ssl/certs/ca-certificates.crt",   # Debian, Ubuntu
    "/etc/pki/tls/certs/ca-bundle.crt",     # Fedora, RHEL
)


def contexto():
    try:
        import certifi
        return ssl.create_default_context(cafile=certifi.where())
    except ImportError:
        pass
    ctx = ssl.create_default_context()
    if ctx.cert_store_stats().get("x509_ca"):
        return ctx
    for caminho in _CANDIDATOS:
        if os.path.exists(caminho):
            return ssl.create_default_context(cafile=caminho)
    return ctx


_CTX = contexto()


def abrir(url, timeout=120):
    req = urllib.request.Request(url, headers=AGENTE)
    return urllib.request.urlopen(req, timeout=timeout, context=_CTX)


def baixar_json(url):
    with abrir(url) as r:
        return json.load(r)


def baixar_texto(url):
    with abrir(url) as r:
        return r.read().decode("utf-8-sig", errors="replace")
