# Define installer name and output file
OutFile "SimpleMon-Installer-v0.5.exe"

# Define the default installation directory
InstallDir $PROGRAMFILES\SimpleMon

# Define the section to install the application
Section "Install SimpleMon"

  # Set output path to the installation directory
  SetOutPath $INSTDIR

  # Copy the executable to the installation directory
  File "path\to\simplemon-windows-amd64.exe"

  # Create a shortcut in the Start Menu
  CreateShortcut "$SMPROGRAMS\SimpleMon.lnk" "$INSTDIR\simplemon-windows-amd64.exe"

  # Create a shortcut on the Desktop
  CreateShortcut "$DESKTOP\SimpleMon.lnk" "$INSTDIR\simplemon-windows-amd64.exe"

  # Register the application as a Windows service
  Exec '"$SYSDIR\sc.exe" create SimpleMon binPath= "$INSTDIR\simplemon-windows-amd64.exe" start= auto'
  Exec '"$SYSDIR\sc.exe" start SimpleMon'

SectionEnd

# Define the section to uninstall the application
Section "Uninstall"

  # Stop and delete the service
  Exec '"$SYSDIR\sc.exe" stop SimpleMon'
  Exec '"$SYSDIR\sc.exe" delete SimpleMon'

  # Remove the executable
  Delete "$INSTDIR\simplemon-windows-amd64.exe"

  # Remove the installation directory if empty
  RMDir "$INSTDIR"

  # Remove the shortcuts
  Delete "$SMPROGRAMS\SimpleMon.lnk"
  Delete "$DESKTOP\SimpleMon.lnk"

SectionEnd
