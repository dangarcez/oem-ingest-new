#Script para pegar os arquivos de métricas especificos do ./cachePack e transformálos em genéricos no ./cachePack2


import glob
import shutil

termo = "./cachePack/cdbp51bc_cdbp51bc1"
not_followed_by = "_anything_gets_by"
# not_followed_by = "_occp40"
tipo_target = "oracle_pdb"
files = glob.glob(f"{termo}*")
files = [f for f in files if not f.startswith(termo+not_followed_by)]

print(files)



for f in files:
   new_file = f.replace(termo,"./cachePack2/"+tipo_target)
   try:
      shutil.copy(f,new_file)
   except PermissionError:
    print("Permission denied.")
   except Exception as e:
      print(f"Error occurred while copying file: {e}")

