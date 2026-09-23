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
; A tela de espera da ATUALIZAÇÃO (ver [Code]): imagem e fontes vão só
; para o {tmp} e não são instaladas. O ícone sai do mesmo SVG do ícone do
; app, gerado pelo scripts/build-windows.sh; as fontes são as mesmas que o
; app embute, para a tela não aparecer com a letra de outro programa.
Source: "..\build\win\espera-icone.bmp"; Flags: dontcopy
Source: "..\cmd\acessos\fontes\IBMPlexSans-SemiBold.ttf"; Flags: dontcopy
Source: "..\cmd\acessos\fontes\IBMPlexMono-Regular.ttf"; Flags: dontcopy

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

[Code]
// ---------------------------------------------------------------------
// A TELA DE ESPERA DA ATUALIZAÇÃO
// ---------------------------------------------------------------------
//
// Só aparece na instalação SILENCIOSA, que é como o atualizador do app
// roda este instalador (/VERYSILENT). Instalação comum tem o assistente do
// Inno na tela e não precisa de nada disto.
//
// POR QUE ELA EXISTE. Na atualização o app é FECHADO pelo Restart Manager
// para os arquivos poderem ser sobrescritos, e só volta no [Run], depois
// da cópia. Medido em 2026-09-23, nesta máquina (rápida): 9,1 segundos com
// NADA na tela. Em máquina lenta é bem mais, e quem está olhando conclui
// que o processo travou — foi relatado exatamente assim.
//
// POR QUE ELA MORA AQUI, e não no app. Nada que rode da pasta instalada
// sobrevive a essa janela de tempo: é justamente o que o Restart Manager
// fecha e o que a cópia substitui. O instalador é o único processo vivo do
// começo ao fim, então é ele quem mostra.
//
// A cara é a do app, não a do Inno: cor de cartão do tema escuro
// (cmd/acessos/tema.go), o ícone gerado do mesmo SVG e as fontes IBM Plex
// que o app embute, carregadas em modo privado só para este processo.

const
  // Paleta do tema escuro do app. O TColor do Windows é BGR, então os
  // valores saem invertidos em relação ao #RRGGBB do tema.go:
  //   Cartao #171b21 -> $211B17     Borda #242a32 -> $322A24
  //   Texto  #d6dde5 -> $E5DDD6     Sec   #94a1ae -> $AEA194
  corCartao = $211B17;
  corBorda  = $322A24;
  corTexto  = $E5DDD6;
  corSec    = $AEA194;
  // Azul de ação do app (#748ffc) e o trilho, um degrau acima do cartão.
  corAzul   = $FC8F74;
  corTrilho = $322A24;
  FR_PRIVATE = $10;
  HWND_TOPMOST = -1;
  SWP_NOSIZE = $1;
  SWP_NOMOVE = $2;
  SWP_NOACTIVATE = $10;

function AddFontResourceEx(lpszFilename: string; fl: DWORD; pdv: Integer): Integer;
  external 'AddFontResourceExW@gdi32.dll stdcall';
function SetWindowPos(hWnd: HWND; hWndInsertAfter: Integer; X, Y, cx, cy: Integer;
  uFlags: UINT): BOOL; external 'SetWindowPos@user32.dll stdcall';

var
  formEspera: TSetupForm;
  trilho, barra: TPanel;

procedure AbrirEspera;
var
  moldura: TPanel;
  img: TBitmapImage;
  titulo, aviso: TNewStaticText;
begin
  ExtractTemporaryFile('espera-icone.bmp');
  ExtractTemporaryFile('IBMPlexSans-SemiBold.ttf');
  ExtractTemporaryFile('IBMPlexMono-Regular.ttf');
  // Privadas: valem só para este processo e não sujam as fontes do sistema
  // de quem está atualizando.
  AddFontResourceEx(ExpandConstant('{tmp}\IBMPlexSans-SemiBold.ttf'), FR_PRIVATE, 0);
  AddFontResourceEx(ExpandConstant('{tmp}\IBMPlexMono-Regular.ttf'), FR_PRIVATE, 0);

  // A assinatura de 4 parâmetros é a do próprio Inno (ver
  // Examples/CodeClasses.iss): largura, altura e as duas travas de
  // redimensionamento — nada aqui cresce, então as duas travadas.
  formEspera := CreateCustomForm(ScaleX(440), ScaleY(232), False, True);
  formEspera.BorderStyle := bsNone;
  formEspera.Position := poScreenCenter;
  formEspera.Color := corBorda;

  // Moldura: o painel interno deixa 1px da cor da borda aparecendo em
  // volta, como o cartão do app.
  moldura := TPanel.Create(formEspera);
  moldura.Parent := formEspera;
  moldura.SetBounds(1, 1, formEspera.ClientWidth - 2, formEspera.ClientHeight - 2);
  moldura.BevelOuter := bvNone;
  moldura.Color := corCartao;
  moldura.ParentBackground := False;

  img := TBitmapImage.Create(formEspera);
  img.Parent := moldura;
  img.Bitmap.LoadFromFile(ExpandConstant('{tmp}\espera-icone.bmp'));
  img.SetBounds((moldura.Width - ScaleX(96)) div 2, ScaleY(30), ScaleX(96), ScaleY(96));

  titulo := TNewStaticText.Create(formEspera);
  titulo.Parent := moldura;
  titulo.Font.Name := 'IBM Plex Sans SemiBold';
  titulo.Font.Size := 13;
  titulo.Font.Color := corTexto;
  titulo.Caption := 'Atualizando o Acessos';
  titulo.AutoSize := True;
  titulo.Left := (moldura.Width - titulo.Width) div 2;
  titulo.Top := ScaleY(144);

  aviso := TNewStaticText.Create(formEspera);
  aviso.Parent := moldura;
  aviso.Font.Name := 'IBM Plex Mono';
  aviso.Font.Size := 9;
  aviso.Font.Color := corSec;
  aviso.Caption := 'Instalando a atualização, aguarde…';
  aviso.AutoSize := True;
  aviso.Left := (moldura.Width - aviso.Width) div 2;
  aviso.Top := ScaleY(174);

  // Barra feita de dois painéis, e não um TNewProgressBar: o controle do
  // Windows ignora Color quando os temas visuais estão ligados e sai
  // verde-sistema no meio de uma tela que é toda do app. Dois painéis
  // pintam na cor que se mandar.
  //
  // E é progresso DE VERDADE: quem move o preenchimento é o
  // CurInstallProgressChanged lá embaixo, que o Inno chama conforme copia.
  trilho := TPanel.Create(formEspera);
  trilho.Parent := moldura;
  trilho.BevelOuter := bvNone;
  trilho.ParentBackground := False;
  trilho.Color := corTrilho;
  trilho.SetBounds(ScaleX(40), ScaleY(196), moldura.Width - ScaleX(80), ScaleY(6));

  barra := TPanel.Create(formEspera);
  barra.Parent := trilho;
  barra.BevelOuter := bvNone;
  barra.ParentBackground := False;
  barra.Color := corAzul;
  barra.SetBounds(0, 0, 0, trilho.Height);

  formEspera.Visible := True;
  // Por cima de tudo, mas SEM roubar o foco de quem estiver digitando.
  SetWindowPos(formEspera.Handle, HWND_TOPMOST, 0, 0, 0, 0,
    SWP_NOMOVE or SWP_NOSIZE or SWP_NOACTIVATE);
end;

// O Inno chama isto conforme copia os arquivos: é o que move a barra.
procedure CurInstallProgressChanged(atual, total: Integer);
begin
  if (formEspera <> nil) and (total > 0) and (trilho <> nil) then
  begin
    barra.Width := (trilho.Width * atual) div total;
    // Sem o Update a barra só anda quando o Windows resolver repintar, e
    // numa cópia rápida ela pula de 0 para cheia de uma vez.
    barra.Update;
  end;
end;

function InitializeSetup: Boolean;
begin
  Result := True;
  if not WizardSilent then
    Exit;
  // A tela é enfeite; a atualização é o que importa. Qualquer tropeço aqui
  // (imagem num formato que o TBitmapImage não leia, fonte que não carregue,
  // Inno futuro que mude um controle) NÃO pode derrubar o instalador: sem
  // isto, um erro de desenho viraria "não consigo mais atualizar".
  try
    AbrirEspera;
  except
    formEspera := nil;
  end;
end;

procedure DeinitializeSetup;
begin
  // Fecha no fim de tudo, DEPOIS do [Run] que reabre o app: assim não há um
  // piscar de tela vazia entre a cópia acabar e a janela do app subir.
  if formEspera <> nil then
    formEspera.Close;
end;
