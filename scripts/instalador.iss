; instalador.iss — instalador do Acessos para Windows.
;
; Não compila nada: empacota a pasta build/win/dist já pronta (o .exe e as
; DLLs que scripts/build-windows.sh resolveu). Rodar por ali:
;
;     scripts/build-windows.sh --instalador
;
; A versão chega de fora (/DAppVersion=X.Y.Z), lida do metainfo — a mesma
; fonte da versão do Flatpak, para os dois nunca divergirem.
#ifndef AppVersion
  #define AppVersion "0.0.0-dev"
#endif

[Setup]
; AppId FIXO: é o que faz o Inno reconhecer "isto já está instalado, é uma
; atualização" em vez de deixar duas cópias. NUNCA gerar outro depois da
; primeira distribuição. O "{{" é a forma de escapar uma chave literal.
AppId={{ed9d32b6-0a74-44b1-9fbe-32becd35bdba}
AppName=Acessos
AppVersion={#AppVersion}
AppPublisher=Machadão
; sem admin: instala no perfil do usuário, como a versão Python fazia.
PrivilegesRequired=lowest
DefaultDirName={localappdata}\Acessos
DefaultGroupName=Acessos
DisableProgramGroupPage=yes
OutputBaseFilename=AcessosSetup-{#AppVersion}
SetupIconFile=..\build\win\acessos.ico
UninstallDisplayIcon={app}\acessos.exe
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
SetupLogging=yes

[Languages]
Name: "brportuguese"; MessagesFile: "compiler:Languages\BrazilianPortuguese.isl"

[Files]
; ignoreversion: a maioria das DLLs de terceiros não tem versão de arquivo
; própria; sobrescrever sempre é mais previsível que deixar o Inno decidir.
Source: "..\build\win\dist\*"; DestDir: "{app}"; Flags: recursesubdirs ignoreversion

[Icons]
Name: "{group}\Acessos"; Filename: "{app}\acessos.exe"; WorkingDir: "{app}"
Name: "{autodesktop}\Acessos"; Filename: "{app}\acessos.exe"; WorkingDir: "{app}"; Tasks: desktopicon

[Tasks]
Name: "desktopicon"; Description: "Criar um atalho na Área de Trabalho"; GroupDescription: "Atalhos adicionais:"

[Run]
Filename: "{app}\acessos.exe"; Description: "Abrir o Acessos agora"; Flags: nowait postinstall skipifsilent

[UninstallDelete]
; o desinstalador só remove o que ele mesmo instalou; o log que o APP
; escreve em tempo de execução ficaria para trás e a pasta não sumiria.
Type: files; Name: "{app}\log.txt"
Type: files; Name: "{app}\log.anterior.txt"
