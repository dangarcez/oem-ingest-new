from fastapi import FastAPI, HTTPException
from fastapi.middleware.cors import CORSMiddleware
from typing import Any, Dict, List, Union
from typing import Optional
import json
import yaml
import json
import msgpack
import oemconnect
import time
import asyncio
import random
from pydantic import BaseModel
from fastapi.responses import JSONResponse

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
value = 1
with open('targets.msgpack','rb') as target_json: #Placeholder para chamada a API
   targets = msgpack.load(target_json)


with open('incidents.msgpack','rb') as f:
    incidents = msgpack.load(f)

with open('incident.msgpack','rb') as f:
    incident = msgpack.load(f)




def find_target(lista: list, chave: str, valor: str):
   resultado = next((item for item in lista if item[chave] == valor), None)
   return resultado



@app.get("/")
def read_root():
    return {"mensagem": "Bem-vindo à API!"}

@app.get("/em/api")
def read_root():    
   return JSONResponse(
      status_code=404,
      content={"detail": f"endpoint não encontrado."},
   )

@app.get("/em/api/targets")
def read_root():
    return {"items":targets}

@app.get("/em/api/incidents")
def read_root():
    return incidents




@app.get("/em/api/incidents/{incident_id}")
def read_root(incident_id : str):    
    return incident




@app.get("/em/api/targets/{target_id}/metricGroups")
def read_root(target_id : str):
    target = find_target(targets,"id",target_id)
    name = target["name"]
    tipo = target["typeName"]
    try:
        with open(f"./cachePack/{name}_metrics","rb")as f:
            resposta = msgpack.load(f)
    except HTTPException as e:
        print("HTTP EXCEPTIONION")
        print(e)
    except Exception as e:
      try:
         with open(f"./cachePack2/{tipo}_metrics","rb")as f:
               resposta = msgpack.load(f)
      except HTTPException as e:
         print("HTTP EXCEPTIONION")
         print(e)
         raise HTTPException(status_code=404, detail="Item not found")
    return resposta

@app.get("/em/api/targets/{target_id}/properties")
def read_root(target_id : str):
    target = find_target(targets,"id",target_id)
    name = target["name"]
    tipo = target["typeName"]
    try:
        with open(f"./cachePack/{name}_properties","rb")as f:
            resposta = msgpack.load(f)
    except HTTPException as e:
        print("HTTP EXCEPTIONION")
        print(e)
    except Exception as e:
      try:
         with open(f"./cachePack2/{tipo}_properties","rb")as f:
               resposta = msgpack.load(f)
      except HTTPException as e:
         print("HTTP EXCEPTIONION")
         print(e)
         raise HTTPException(status_code=404, detail="Item not found")
    return resposta

@app.get("/em/api/targets/{target_id}/metricGroups/{group_name}/latestData")
async def read_roottres(target_id : str,group_name: str,page : str = ""):
    target = find_target(targets,"id",target_id)
    name = target["name"]
    tipo = target["typeName"]
    
   #  #simulando lag
   #  if group_name == "Response":
   #      print("..............SLEEPING.................")
   #      await asyncio.sleep(5)
    #simulando names que sao deletados


        
    try:
        with open(f"./cachePack/{name}_{group_name}_metric","rb")as f:
            resposta = msgpack.load(f)
            if group_name == "topWaitEvents":
                intrandom = random.randint(0,3)
                print(f"deleted: {resposta['items'][intrandom]}")
                del resposta["items"][intrandom]
    except Exception as e:
        try:
            with open(f"./cachePack2/{tipo}_{group_name}_metric","rb")as f:
               resposta = msgpack.load(f)
               if group_name == "topWaitEvents":
                  intrandom = random.randint(0,3)
                  print(f"deleted: {resposta['items'][intrandom]}")
                  del resposta["items"][intrandom]
        except Exception as e:
         raise HTTPException(status_code=404, detail="Item not found")
        
    if(page!=""):
      del resposta["links"]["next"]
   #  print(resposta)
    return resposta

@app.get("/em/api/targets/{target_id}/metricGroups/{group_name}")
def read_root(target_id : str,group_name: str):
    target = find_target(targets,"id",target_id)
    name = target["name"]
    tipo = target["typeName"]
   #  with open(f"./cachePack/{name}_metrics","rb")as f:
   #    resposta = msgpack.load(f)    
   #  resposta = find_target(resposta["items"],"name",group_name)
    try:
      with open(f"./cachePack/{name}_metrics","rb")as f:
         resposta = msgpack.load(f)    
      resposta = find_target(resposta["items"],"name",group_name)
    except Exception as e:
      try:
         with open(f"./cachePack2/{tipo}_metrics","rb")as f:
            resposta = msgpack.load(f)    
         resposta = find_target(resposta["items"],"name",group_name)
      except Exception as e:
         raise HTTPException(status_code=404, detail="Item not found")
    return resposta

@app.get("/em/api/targets/{target_id}/metricGroups/{group_name}")
def read_root(target_id : str,group_name: str):
    target = find_target(targets,"id",target_id)
    name = target["name"]
    tipo = target["typeName"]
    try:
      with open(f"./cachePack/{name}_metrics","rb")as f:
         resposta = msgpack.load(f)    
    except Exception as e:        
      try:
         with open(f"./cachePack2/{tipo}_metrics","rb")as f:
            resposta = msgpack.load(f)   
      except:
         raise HTTPException(status_code=404, detail="Item not found")
      
    resposta = find_target(resposta["items"],"name",group_name)
    return resposta


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
