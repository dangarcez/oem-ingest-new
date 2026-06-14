import json
import socket
import subprocess
import sys
import time
import unittest
import urllib.error
import urllib.request
from pathlib import Path


BASE_DIR = Path(__file__).resolve().parent


class OEMMockAPITest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.port = free_port()
        cls.base_url = f"http://127.0.0.1:{cls.port}"
        cls.server = subprocess.Popen(
            [
                sys.executable,
                "-m",
                "uvicorn",
                "api:app",
                "--host",
                "127.0.0.1",
                "--port",
                str(cls.port),
                "--log-level",
                "warning",
            ],
            cwd=BASE_DIR,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )
        cls.wait_until_ready()

    @classmethod
    def tearDownClass(cls):
        if not hasattr(cls, "server"):
            return
        cls.server.terminate()
        try:
            cls.server.wait(timeout=5)
        except subprocess.TimeoutExpired:
            cls.server.kill()
            cls.server.wait(timeout=5)

    @classmethod
    def wait_until_ready(cls):
        deadline = time.monotonic() + 10
        last_error = None
        while time.monotonic() < deadline:
            if cls.server.poll() is not None:
                _, stderr = cls.server.communicate(timeout=1)
                raise RuntimeError(f"mock exited before startup: {stderr}")
            try:
                response = request(cls.base_url, "GET", "/em/api")
                if response.status == 200:
                    return
            except OSError as exc:
                last_error = exc
            time.sleep(0.1)
        raise RuntimeError(f"mock did not start: {last_error}")

    def test_otlp_stubs_accept_binary_payloads(self):
        payload = b"\x00\x01otlp-protobuf"

        metrics = request(
            self.base_url,
            "POST",
            "/v1/metrics",
            payload,
            {"Content-Type": "application/x-protobuf"},
        )
        logs = request(
            self.base_url,
            "POST",
            "/v1/logs",
            payload,
            {"Content-Type": "application/x-protobuf"},
        )

        self.assertEqual(metrics.status, 200)
        self.assertEqual(logs.status, 200)
        self.assertEqual(json.loads(metrics.body), {"accepted": True, "bytes": len(payload)})
        self.assertEqual(json.loads(logs.body), {"accepted": True, "bytes": len(payload)})

    def test_existing_oem_endpoints_still_respond(self):
        api = request(self.base_url, "GET", "/em/api")
        self.assertEqual(api.status, 200)
        self.assertEqual(json.loads(api.body)["name"], "oem-mock")

        targets = request(self.base_url, "GET", "/em/api/targets")
        target_items = json.loads(targets.body)["items"]
        self.assertGreater(len(target_items), 0)

        incidents = request(
            self.base_url,
            "GET",
            "/em/api/incidents/?ageInHoursLessThanOrEqualTo=1",
        )
        self.assertEqual(incidents.status, 200)
        self.assertIn("items", json.loads(incidents.body))

        target_id = "007FCBCC1AECBAE3831390E244127549"

        properties = request(self.base_url, "GET", f"/em/api/targets/{target_id}/properties")
        self.assertEqual(properties.status, 200)
        self.assertIn("items", json.loads(properties.body))

        groups = request(self.base_url, "GET", f"/em/api/targets/{target_id}/metricGroups")
        self.assertEqual(groups.status, 200)
        group_items = json.loads(groups.body)["items"]
        self.assertGreater(len(group_items), 0)

        group = request(
            self.base_url,
            "GET",
            f"/em/api/targets/{target_id}/metricGroups/CCCData_ORACLE_obs_delayed",
        )
        self.assertEqual(json.loads(group.body)["name"], "CCCData_ORACLE_obs_delayed")

        latest = request(
            self.base_url,
            "GET",
            f"/em/api/targets/{target_id}/metricGroups/Response/latestData?limit=200",
        )
        latest_body = json.loads(latest.body)
        self.assertEqual(latest.status, 200)
        self.assertEqual(latest_body["metricGroupName"], "Response")
        self.assertGreater(len(latest_body["items"]), 0)

        incident_id = json.loads(incidents.body)["items"][0]["id"]
        incident = request(self.base_url, "GET", f"/em/api/incidents/{incident_id}")
        self.assertEqual(incident.status, 200)
        self.assertEqual(json.loads(incident.body)["id"], incident_id)

        second_incident_id = json.loads(incidents.body)["items"][1]["id"]
        second_incident = request(self.base_url, "GET", f"/em/api/incidents/{second_incident_id}")
        self.assertEqual(second_incident.status, 200)
        self.assertEqual(json.loads(second_incident.body)["id"], second_incident_id)

    def test_unknown_target_returns_404(self):
        response = request(
            self.base_url,
            "GET",
            "/em/api/targets/missing-target/metricGroups",
        )

        self.assertEqual(response.status, 404)

    def test_unknown_incident_returns_404(self):
        response = request(
            self.base_url,
            "GET",
            "/em/api/incidents/missing-incident",
        )

        self.assertEqual(response.status, 404)


class Response:
    def __init__(self, status, body):
        self.status = status
        self.body = body


def request(base_url, method, path, data=None, headers=None):
    req = urllib.request.Request(
        base_url + path,
        data=data,
        headers=headers or {},
        method=method,
    )
    try:
        with urllib.request.urlopen(req, timeout=5) as response:
            return Response(response.status, response.read().decode("utf-8"))
    except urllib.error.HTTPError as exc:
        return Response(exc.code, exc.read().decode("utf-8"))


def free_port():
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


if __name__ == "__main__":
    unittest.main()
