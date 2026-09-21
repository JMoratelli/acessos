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
AppPublisher=Jurandir Moratelli
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
; O atualizador roda este instalador com /VERYSILENT enquanto o próprio
; acessos.exe está de pé, e NÃO fecha o app ao disparar: quem fecha é o
; Restart Manager, daqui, na hora de sobrescrever o arquivo. Isso é de
; propósito — com /VERYSILENT não há janela nenhuma na tela, e o app
; saindo na hora deixaria o usuário olhando para o nada durante toda a
; cópia. Segurando a janela dele até este ponto, o que fica no ar é a UI
; do próprio Acessos ("Instalando a atualização…"), como no Linux, onde o
; flatpak install não precisa derrubar ninguém para instalar.
; Portanto CloseApplications NÃO é mais rede de segurança, é o caminho
; normal (continua sendo o padrão do Inno 6, mas aqui é explícito porque
; agora se depende dele).
;
; Reabrir depois é o [Run] logo abaixo, e não RestartApplications: esse só
; reabre o que o Restart Manager fechou, e o app também pode ter saído por
; conta própria (o usuário fechando a janela no meio) — o [Run] cobre os
; dois casos.
CloseApplications=yes
RestartApplications=no

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
; skipifsilent: some quando a instalação é silenciosa — é o caso normal,
; interativo, com a caixa "abrir agora" ao final.
Filename: "{app}\acessos.exe"; Description: "Abrir o Acessos agora"; Flags: nowait postinstall skipifsilent
; skipifnotsilent: o complemento, para o atualizador (que SEMPRE roda
; /VERYSILENT) — reabre sem perguntar nada, já que não há tela para
; perguntar.
Filename: "{app}\acessos.exe"; Flags: nowait skipifnotsilent

[UninstallDelete]
; o desinstalador só remove o que ele mesmo instalou; o log que o APP
; escreve em tempo de execução ficaria para trás e a pasta não sumiria.
Type: files; Name: "{app}\log.txt"
Type: files; Name: "{app}\log.anterior.txt"
