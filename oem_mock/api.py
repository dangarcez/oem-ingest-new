from pathlib import Path
from fastapi import FastAPI, HTTPException, Request
from fastapi.middleware.cors import CORSMiddleware
from typing import Any, Dict, List, Union
import msgpack
import random
from pydantic import BaseModel
from fastapi.responses import JSONResponse

BASE_DIR = Path(__file__).resolve().parent
CACHE_BY_NAME_DIR = BASE_DIR / "cachePack"
CACHE_BY_TYPE_DIR = BASE_DIR / "cachePack2"

app = FastAPI()
origins = [
    "*"  # Allows all origins
]

app.add_middleware(
    CORSMiddleware,
    allow_origins=origins,
    allow_credentials=True,
    allow_methods=["*"],  # Allows all methods (GET, POST, PUT, DELETE, etc.)
    allow_headers=["*"],  # Allows all headers
)

def load_msgpack(path: Path):
    with path.open("rb") as file:
        return msgpack.load(file)


targets = load_msgpack(BASE_DIR / "targets.msgpack")
incidents = load_msgpack(BASE_DIR / "incidents.msgpack")
incident = load_msgpack(BASE_DIR / "incident.msgpack")


def find_target(lista: list, chave: str, valor: str):
   return next((item for item in lista if item.get(chave) == valor), None)


def target_or_404(target_id: str):
   target = find_target(targets, "id", target_id)
   if target is None:
      raise HTTPException(status_code=404, detail="Target not found")
   return target


def incident_or_404(incident_id: str):
   if incident.get("id") == incident_id:
      return incident

   found = find_target(incidents.get("items", []), "id", incident_id)
   if found is None:
      raise HTTPException(status_code=404, detail="Incident not found")
   return found


def load_target_fixture(target: dict, suffix: str):
   name_path = CACHE_BY_NAME_DIR / f"{target['name']}_{suffix}"
   type_path = CACHE_BY_TYPE_DIR / f"{target['typeName']}_{suffix}"

   for path in (name_path, type_path):
      try:
         return load_msgpack(path)
      except FileNotFoundError:
         continue

   raise HTTPException(status_code=404, detail="Item not found")


def load_metric_group(target: dict, group_name: str):
   groups = load_target_fixture(target, "metrics")
   group = find_target(groups.get("items", []), "name", group_name)
   if group is None:
      raise HTTPException(status_code=404, detail="Item not found")
   return group


async def accept_otlp_payload(request: Request):
   body = await request.body()
   return JSONResponse(status_code=200, content={"accepted": True, "bytes": len(body)})



@app.get("/")
def read_root():
    return {"mensagem": "Bem-vindo à API!"}

@app.get("/em/api")
def read_root():    
   return {"name": "oem-mock", "version": "local"}

@app.get("/em/api/targets")
def read_root():
    return {"items":targets}

@app.get("/em/api/incidents")
@app.get("/em/api/incidents/")
def read_root():
    return incidents




@app.get("/em/api/incidents/{incident_id}")
def read_root(incident_id : str):    
    return incident_or_404(incident_id)




@app.get("/em/api/targets/{target_id}/metricGroups")
def read_root(target_id : str):
    target = target_or_404(target_id)
    return load_target_fixture(target, "metrics")

@app.get("/em/api/targets/{target_id}/properties")
def read_root(target_id : str):
    target = target_or_404(target_id)
    return load_target_fixture(target, "properties")

@app.get("/em/api/targets/{target_id}/metricGroups/{group_name}/latestData")
async def read_roottres(target_id : str,group_name: str,page : str = ""):
    target = target_or_404(target_id)

   #  #simulando lag
   #  if group_name == "Response":
   #      print("..............SLEEPING.................")
   #      await asyncio.sleep(5)
    #simulando names que sao deletados

    resposta = load_target_fixture(target, f"{group_name}_metric")
    if group_name == "topWaitEvents" and resposta.get("items"):
        intrandom = random.randint(0, min(3, len(resposta["items"]) - 1))
        print(f"deleted: {resposta['items'][intrandom]}")
        del resposta["items"][intrandom]
        
    if(page!=""):
      resposta.get("links", {}).pop("next", None)
   #  print(resposta)
    return resposta

@app.get("/em/api/targets/{target_id}/metricGroups/{group_name}")
def read_root(target_id : str,group_name: str):
    target = target_or_404(target_id)
    return load_metric_group(target, group_name)


@app.post("/v1/metrics")
async def otlp_metrics(request: Request):
    return await accept_otlp_payload(request)


@app.post("/v1/logs")
async def otlp_logs(request: Request):
    return await accept_otlp_payload(request)


class UserCreate(BaseModel):
    mensagem: str

@app.post("/teams/")
def read_teams_message(use_data: UserCreate):
    print(use_data.mensagem)
    return {"message": use_data.mensagem}

@app.post("/posting")
async def handle_unstructured_json(request_body: Union[List, Dict, Any] = None):
    """
    Handles a POST request with an arbitrary JSON body.
    The request_body can be a list, dictionary, or any other valid JSON type.
    """
   #  print(request_body)
    return {"received_data": request_body}
