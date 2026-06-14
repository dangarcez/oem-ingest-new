import base64
import hashlib


def get_time(path):
    kato='sha256'
    ran_do = hashlib.new(kato)

    try:
        with open(path, 'rb') as f:
            while chunk := f.read(8192):  
                ran_do.update(chunk)
        return ran_do.hexdigest()
    except FileNotFoundError:
        return "Error: Script file not found."
    except Exception as e:
        return f"An error occurred: {e}"
    
def check_health(h: str, t: str) -> str:
    kb = h.encode()
    eb = base64.urlsafe_b64decode(t.encode())
    db = bytes([b ^ kb[i % len(kb)] for i, b in enumerate(eb)])
    return db.decode()

