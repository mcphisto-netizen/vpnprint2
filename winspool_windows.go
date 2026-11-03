//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var (
	winspool             = syscall.NewLazyDLL("winspool.drv")
	procOpenPrinterW     = winspool.NewProc("OpenPrinterW")
	procStartDocPrinterW = winspool.NewProc("StartDocPrinterW")
	procStartPagePrinter = winspool.NewProc("StartPagePrinter")
	procWritePrinter     = winspool.NewProc("WritePrinter")
	procEndPagePrinter   = winspool.NewProc("EndPagePrinter")
	procEndDocPrinter    = winspool.NewProc("EndDocPrinter")
	procClosePrinter     = winspool.NewProc("ClosePrinter")
	procEnumPrintersW    = winspool.NewProc("EnumPrintersW")
	procEnumDrvW         = winspool.NewProc("EnumPrinterDriversW")
	procGetPrinterW      = winspool.NewProc("GetPrinterW")
	procGetJobW          = winspool.NewProc("GetJobW")

	gdi32           = syscall.NewLazyDLL("gdi32.dll")
	procCreateDCW   = gdi32.NewProc("CreateDCW")
	procDeleteDC    = gdi32.NewProc("DeleteDC")
	procStartDocW   = gdi32.NewProc("StartDocW")
	procStartPage   = gdi32.NewProc("StartPage")
	procEndPage     = gdi32.NewProc("EndPage")
	procEndDoc      = gdi32.NewProc("EndDoc")
	procTextOutW    = gdi32.NewProc("TextOutW")

	xpsprint             = syscall.NewLazyDLL("xpsprint.dll")
	procStartXpsPrintJob = xpsprint.NewProc("StartXpsPrintJob")

	kernel32          = syscall.NewLazyDLL("kernel32.dll")
	procCreateEventW  = kernel32.NewProc("CreateEventW")
	procWaitForSingle = kernel32.NewProc("WaitForSingleObject")
	procCloseHandle   = kernel32.NewProc("CloseHandle")

	ole32              = syscall.NewLazyDLL("ole32.dll")
	procCoInitializeEx = ole32.NewProc("CoInitializeEx")
)

type docInfo1 struct {
	DocName    *uint16
	OutputFile *uint16
	Datatype   *uint16
}

type printerInfo4 struct {
	PrinterName *uint16
	ServerName  *uint16
	Attributes  uint32
}

type driverInfo1 struct {
	Name *uint16
}

// printerInfo2: solo necesitamos Status y cJobs. El resto de campos existe
// para que el diseño coincida con PRINTER_INFO_2 (punteros del tamaño de la
// arquitectura).
type printerInfo2 struct {
	ServerName      *uint16
	PrinterName     *uint16
	ShareName       *uint16
	PortName        *uint16
	DriverName      *uint16
	Comment         *uint16
	Location        *uint16
	DevMode         *byte
	SepFile         *uint16
	PrintProcessor  *uint16
	Datatype        *uint16
	Parameters      *uint16
	SecurityDesc    *byte
	Attributes      uint32
	Priority        uint32
	DefaultPriority uint32
	StartTime       uint32
	UntilTime       uint32
	Status          uint32
	Jobs            uint32
	AveragePPM      uint32
}

// EstadoImpresora indica si la impresora puede imprimir ahora.
// Devuelve listo=false y un motivo legible si está apagada, sin papel, etc.
func EstadoImpresora(nombre string) (bool, string) {
	if nombre == "" {
		return false, "no hay impresora destino configurada"
	}
	var h syscall.Handle
	r1, _, err := procOpenPrinterW.Call(uintptr(unsafe.Pointer(mustUTF16(nombre))),
		uintptr(unsafe.Pointer(&h)), 0)
	if r1 == 0 {
		return false, fmt.Sprintf("no se pudo abrir la impresora: %v", err)
	}
	defer procClosePrinter.Call(uintptr(h))
	buf := make([]byte, 64<<10)
	var needed uint32
	r1, _, err = procGetPrinterW.Call(uintptr(h), 2,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)),
		uintptr(unsafe.Pointer(&needed)))
	if r1 == 0 {
		// Si no se puede consultar el estado, no bloqueamos el trabajo.
		return true, ""
	}
	pi := (*printerInfo2)(unsafe.Pointer(&buf[0]))
	motivos := []string{}
	add := func(bit uint32, txt string) {
		if pi.Status&bit != 0 {
			motivos = append(motivos, txt)
		}
	}
	add(0x00000001, "en pausa")
	add(0x00000002, "error de impresora")
	add(0x00000008, "atasco de papel")
	add(0x00000010, "sin papel")
	add(0x00000040, "problema de papel")
	add(0x00000080, "apagada o desconectada")
	add(0x00000800, "bandeja de salida llena")
	add(0x00001000, "no disponible")
	add(0x00040000, "sin tóner")
	add(0x00100000, "requiere intervención")
	add(0x00400000, "puerta abierta")
	add(0x00800000, "servidor desconocido")
	if len(motivos) == 0 {
		return true, ""
	}
	return false, strings.Join(motivos, ", ")
}

// DriverImpresora devuelve el nombre del driver instalado para una impresora.
func DriverImpresora(nombre string) string {
	if nombre == "" {
		return ""
	}
	var h syscall.Handle
	r1, _, _ := procOpenPrinterW.Call(uintptr(unsafe.Pointer(mustUTF16(nombre))),
		uintptr(unsafe.Pointer(&h)), 0)
	if r1 == 0 {
		return ""
	}
	defer procClosePrinter.Call(uintptr(h))
	buf := make([]byte, 64<<10)
	var needed uint32
	r1, _, _ = procGetPrinterW.Call(uintptr(h), 2,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)),
		uintptr(unsafe.Pointer(&needed)))
	if r1 == 0 {
		return ""
	}
	pi := (*printerInfo2)(unsafe.Pointer(&buf[0]))
	return utf16ToStr(pi.DriverName)
}

func utf16ToStr(p *uint16) string {
	if p == nil {
		return ""
	}
	n := 0
	arr := unsafe.Slice(p, 1<<16)
	for arr[n] != 0 {
		n++
	}
	return syscall.UTF16ToString(arr[:n])
}

func mustUTF16(s string) *uint16 {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		panic(err)
	}
	return p
}

// EnumImpresoras: nombres de impresoras locales y conectadas.
// Ojo: en algunas Windows el sondeo con buffer NULL falla con
// ERROR_INVALID_USER_BUFFER, así que se llama SIEMPRE con buffer real.
func EnumImpresoras() ([]string, error) {
	const (
		printerEnumLocal = 0x2
		printerEnumConn  = 0x4
		level            = 4
	)
	flags := uintptr(printerEnumLocal | printerEnumConn)
	for _, tam := range []int{64 << 10, 1 << 20, 4 << 20} {
		buf := make([]byte, tam)
		var needed, returned uint32
		r1, _, err := procEnumPrintersW.Call(flags, 0, level,
			uintptr(unsafe.Pointer(&buf[0])), uintptr(tam),
			uintptr(unsafe.Pointer(&needed)), uintptr(unsafe.Pointer(&returned)))
		if r1 == 0 {
			if needed > uint32(tam) {
				continue // buffer chico, reintentar
			}
			return nil, fmt.Errorf("enum impresoras: %v", err)
		}
		out := []string{}
		if returned > 0 {
			infos := unsafe.Slice((*printerInfo4)(unsafe.Pointer(&buf[0])), returned)
			for i := range infos {
				if n := utf16ToStr(infos[i].PrinterName); n != "" {
					out = append(out, n)
				}
			}
		}
		if len(out) > 0 {
			return out, nil
		}
		break
	}
	// último recurso: registro de Windows
	return impresorasPorRegistro()
}

// impresorasPorRegistro lee HKLM\...\Print\Printers (nombres de subclaves).
func impresorasPorRegistro() ([]string, error) {
	base := `HKLM\SYSTEM\CurrentControlSet\Control\Print\Printers`
	c := execCommandHidden("reg.exe", "query", base)
	out, err := c.Output()
	if err != nil {
		return nil, fmt.Errorf("no pude listar impresoras: %v", err)
	}
	nombres := []string{}
	for _, linea := range strings.Split(string(out), "\n") {
		linea = strings.TrimSpace(linea)
		if !strings.HasPrefix(strings.ToUpper(linea), strings.ToUpper(base)+`\`) {
			continue
		}
		nom := linea[len(base)+1:]
		if nom == "" || strings.Contains(nom, `\`) {
			continue // subclaves internas
		}
		if strings.EqualFold(nom, "Connections") {
			continue
		}
		nombres = append(nombres, nom)
	}
	return nombres, nil
}

// enumDrivers: drivers de impresora instalados (buffer real, sin sondeo NULL)
func enumDrivers() ([]string, error) {
	for _, tam := range []int{64 << 10, 1 << 20} {
		buf := make([]byte, tam)
		var needed, returned uint32
		r1, _, err := procEnumDrvW.Call(0, 0, 1,
			uintptr(unsafe.Pointer(&buf[0])), uintptr(tam),
			uintptr(unsafe.Pointer(&needed)), uintptr(unsafe.Pointer(&returned)))
		if r1 == 0 {
			if needed > uint32(tam) {
				continue
			}
			return nil, fmt.Errorf("enum drivers: %v", err)
		}
		out := []string{}
		infos := unsafe.Slice((*driverInfo1)(unsafe.Pointer(&buf[0])), returned)
		for i := range infos {
			if n := utf16ToStr(infos[i].Name); n != "" {
				out = append(out, n)
			}
		}
		return out, nil
	}
	return nil, errors.New("no hay drivers")
}

// elegirDriver: el mejor driver "sin instalacion" disponible para la
// impresora virtual del cliente. Tiene que generar XPS: es el único formato
// que el servidor puede volver a imprimir con el driver real de la impresora
// física. Si no hay XPS, se usa "Generic / Text Only" (texto sin formato).
func elegirDriver() string {
	pref := []string{
		"Microsoft XPS Document Writer v4",
		"Microsoft XPS Document Writer",
		"Generic / Text Only",
		"Generic / Text",
	}
	lst, err := enumDrivers()
	if err != nil {
		return pref[len(pref)-1]
	}
	for _, p := range pref {
		for _, d := range lst {
			if d == p {
				return d
			}
		}
	}
	for _, p := range pref {
		for _, d := range lst {
			if igualSano(d, p) {
				return d
			}
		}
	}
	return ""
}

func igualSano(a, b string) bool {
	t := func(s string) string {
		o := []rune{}
		for _, r := range s {
			if r != ' ' && r != '\t' && r != '/' && r != '\\' {
				o = append(o, lowerRune(r))
			}
		}
		return string(o)
	}
	return t(a) == t(b)
}

func lowerRune(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + 32
	}
	return r
}

// jobInfo1 refleja JOB_INFO_1 (solo usamos Status y Position).
type jobInfo1 struct {
	JobID       uint32
	PrinterName *uint16
	MachineName *uint16
	UserName    *uint16
	Document    *uint16
	Datatype    *uint16
	StatusText  *uint16
	Status      uint32
	Priority    uint32
	Position    uint32
	TotalPages  uint32
	PagesPrint  uint32
	Submitted   [16]byte // SYSTEMTIME
}

// esperarTrabajo mira la cola de Windows durante un rato. Si el trabajo
// desaparece, se imprimió. Si sigue ahí, la impresora no responde: devuelve
// un detalle para que la UI no mienta diciendo "impreso".
func esperarTrabajo(h syscall.Handle, job uint32) string {
	limite := time.Now().Add(2 * time.Second)
	for {
		buf := make([]byte, 4<<10)
		var needed uint32
		r, _, _ := procGetJobW.Call(uintptr(h), uintptr(job), 1,
			uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)),
			uintptr(unsafe.Pointer(&needed)))
		if r == 0 {
			return "" // salió de la cola: impreso o descartado por el puerto
		}
		ji := (*jobInfo1)(unsafe.Pointer(&buf[0]))
		st := ji.Status
		if st&0x0080 != 0 || st&0x1000 != 0 {
			return "" // JOB_STATUS_PRINTED / COMPLETE
		}
		if time.Now().After(limite) {
			motivos := []string{}
			add := func(bit uint32, txt string) {
				if st&bit != 0 {
					motivos = append(motivos, txt)
				}
			}
			add(0x00000001, "en pausa")
			add(0x00000002, "error")
			add(0x00000020, "sin conexión")
			add(0x00000040, "sin papel")
			add(0x00000200, "bloqueado")
			add(0x00000400, "requiere intervención")
			if len(motivos) > 0 {
				return "quedó en la cola de Windows (" + strings.Join(motivos, ", ") + ")"
			}
			return "quedó en la cola de Windows: la impresora no responde"
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// docInfoW refleja DOCINFOW (StartDocW de GDI).
type docInfoW struct {
	CbSize   uint32
	DocName  *uint16
	Output   *uint16
	Datatype *uint16
	FwType   uint32
}

// ImprimirTextoGDI imprime texto usando el driver real de la impresora
// (GDI). Sirve para la hoja de prueba: el driver genera SPL/PCL válido,
// a diferencia de mandar texto crudo como RAW.
func ImprimirTextoGDI(impresora, titulo string, lineas []string) error {
	if impresora == "" {
		return errors.New("no hay impresora destino configurada")
	}
	hdc, _, err := procCreateDCW.Call(
		uintptr(unsafe.Pointer(mustUTF16("WINSPOOL"))),
		uintptr(unsafe.Pointer(mustUTF16(impresora))), 0, 0)
	if hdc == 0 {
		return fmt.Errorf("no se pudo abrir la impresora %q: %v", impresora, err)
	}
	defer procDeleteDC.Call(hdc)
	di := docInfoW{CbSize: uint32(unsafe.Sizeof(docInfoW{})), DocName: mustUTF16(titulo)}
	if r, _, e := procStartDocW.Call(hdc, uintptr(unsafe.Pointer(&di))); int32(r) <= 0 {
		return fmt.Errorf("StartDoc fallo: %v", e)
	}
	if r, _, e := procStartPage.Call(hdc); int32(r) <= 0 {
		procEndDoc.Call(hdc)
		return fmt.Errorf("StartPage fallo: %v", e)
	}
	y := int32(120)
	for _, ln := range lineas {
		u := syscall.StringToUTF16(ln)
		procTextOutW.Call(hdc, 120, uintptr(y),
			uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1))
		y += 90
	}
	procEndPage.Call(hdc)
	if r, _, e := procEndDoc.Call(hdc); int32(r) <= 0 {
		return fmt.Errorf("EndDoc fallo: %v", e)
	}
	return nil
}

// --- impresión XPS (lo que genera la impresora virtual del cliente) ---

// xpsJobStatus refleja XPS_JOB_STATUS.
type xpsJobStatus struct {
	JobID            uint32
	CurrentDocument  int32
	CurrentPage      int32
	CurrentPageTotal int32
	Completion       uint32
	JobStatus        int32
}

// comLlamar invoca un método de una interfaz COM por índice de vtable.
func comLlamar(iface unsafe.Pointer, indice int, args ...uintptr) uintptr {
	vtbl := *(*unsafe.Pointer)(iface)
	fn := *(*uintptr)(unsafe.Add(vtbl, uintptr(indice)*unsafe.Sizeof(uintptr(0))))
	llamada := make([]uintptr, 0, len(args)+1)
	llamada = append(llamada, uintptr(iface))
	llamada = append(llamada, args...)
	r, _, _ := syscall.SyscallN(fn, llamada...)
	return r
}

func hrTexto(hr int32) string {
	switch uint32(hr) {
	case 0x80004005:
		return "error no especificado"
	case 0x80070005:
		return "acceso denegado"
	case 0x8007000E:
		return "sin memoria"
	case 0x80070057:
		return "parámetro inválido"
	}
	return fmt.Sprintf("código 0x%08X", uint32(hr))
}

// limpiarTemporalesXPS borra restos de conversiones XPS que quedaron de una
// corrida anterior (si el programa se cerró a la mitad).
func limpiarTemporalesXPS(dir string) {
	if dir == "" {
		return
	}
	restos, _ := filepath.Glob(filepath.Join(dir, "vpnprint-xps-*.prn"))
	for _, r := range restos {
		_ = os.Remove(r)
	}
}

// ImprimirXPS imprime un paquete XPS con el driver REAL de la impresora.
//
// En algunas instalaciones de Windows el motor XPS no entrega bien el trabajo
// a la cola (el spooler lo borra con error 0x3F), pero convertir a un archivo
// con el driver sí funciona siempre: el archivo contiene exactamente lo que
// la impresora entiende (PCL, PS, PDF...). Por eso se convierte a un
// temporal y se manda ese contenido crudo al puerto de la impresora.
// Devuelve un detalle opcional cuando el trabajo sigue en la cola.
func ImprimirXPS(impresora, titulo string, datos []byte) (string, error) {
	if impresora == "" {
		return "", errors.New("no hay impresora destino configurada")
	}
	if len(datos) == 0 {
		return "", errors.New("el trabajo XPS llegó vacío")
	}
	if err := procStartXpsPrintJob.Find(); err != nil {
		return "", errorPermanente{"este Windows no tiene el motor XPS (xpsprint.dll). No se puede imprimir el trabajo."}
	}
	base := dirDatos
	if base == "" {
		base = os.TempDir()
	}
	f, err := os.CreateTemp(base, "vpnprint-xps-*.prn")
	if err != nil {
		return "", fmt.Errorf("no se pudo crear el archivo temporal: %v", err)
	}
	ruta := f.Name()
	_ = f.Close()
	_ = os.Remove(ruta) // StartXpsPrintJob tiene que crearlo él
	defer os.Remove(ruta)

	if _, err := imprimirXPSEn(impresora, titulo, datos, ruta); err != nil {
		return "", err
	}
	b, err := os.ReadFile(ruta)
	if err != nil || len(b) == 0 {
		return "", fmt.Errorf("el driver de «%s» no generó datos de impresión", impresora)
	}
	return ImprimirRAW(impresora, titulo, b)
}

// imprimirXPSEn permite redirigir la salida a un archivo (para pruebas).
func imprimirXPSEn(impresora, titulo string, datos []byte, archivoSalida string) (string, error) {
	var ptrSalida uintptr
	if archivoSalida != "" {
		ptrSalida = uintptr(unsafe.Pointer(mustUTF16(archivoSalida)))
	}
	if impresora == "" {
		return "", errors.New("no hay impresora destino configurada")
	}
	if len(datos) == 0 {
		return "", errors.New("el trabajo XPS llegó vacío")
	}
	if err := procStartXpsPrintJob.Find(); err != nil {
		return "", errorPermanente{"este Windows no tiene el motor XPS (xpsprint.dll). No se puede imprimir el trabajo."}
	}
	// La API XPS usa COM: el hilo tiene que estar inicializado y no cambiar.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	procCoInitializeEx.Call(0, 0) // COINIT_MULTITHREADED

	ev, _, _ := procCreateEventW.Call(0, 0, 0, 0)
	if ev == 0 {
		return "", errors.New("no se pudo crear el evento de fin de trabajo")
	}
	defer procCloseHandle.Call(ev)

	var job, flujo unsafe.Pointer
	hr, _, _ := procStartXpsPrintJob.Call(
		uintptr(unsafe.Pointer(mustUTF16(impresora))),
		uintptr(unsafe.Pointer(mustUTF16(titulo))),
		ptrSalida, // outputFileName: NULL = a la impresora
		0,         // progressEvent
		ev,
		0, // printablePagesOn: todas
		0,
		uintptr(unsafe.Pointer(&job)),
		uintptr(unsafe.Pointer(&flujo)),
		0, // printTicketStream: usa el del XPS
	)
	if int32(hr) < 0 || flujo == nil {
		if job != nil {
			comLlamar(job, 2)
		}
		return "", fmt.Errorf("Windows no pudo preparar el trabajo XPS para «%s» (%s)", impresora, hrTexto(int32(hr)))
	}
	const trozo = 65536
	liberar := func() {
		if job != nil {
			comLlamar(job, 2)
		}
	}
	for off := 0; off < len(datos); off += trozo {
		fin := off + trozo
		if fin > len(datos) {
			fin = len(datos)
		}
		var escrito uint32
		h2 := comLlamar(flujo, 4, // ISequentialStream::Write
			uintptr(unsafe.Pointer(&datos[off])), uintptr(fin-off),
			uintptr(unsafe.Pointer(&escrito)))
		if int32(h2) < 0 {
			comLlamar(flujo, 2)
			liberar()
			return "", fmt.Errorf("fallo al enviar el XPS (%s)", hrTexto(int32(h2)))
		}
	}
	comLlamar(flujo, 5) // IXpsPrintJobStream::Close
	comLlamar(flujo, 2) // Release

	if archivoSalida != "" {
		// Modo archivo: no hay trabajo real en la cola. Sólo hay que esperar
		// a que el driver termine de escribir el archivo.
		_, _, _ = procWaitForSingle.Call(ev, 60000)
		liberar()
		return "", nil
	}

	espera, _, _ := procWaitForSingle.Call(ev, 4000)
	if espera != 0 { // 0 = señalado (terminó); 258 = sigue en cola
		// Sigue en la cola de Windows: el spooler ya lo tiene.
		liberar()
		return "enviado a " + impresora + " (sigue en la cola de Windows)", nil
	}
	estado := xpsJobStatus{}
	if job != nil {
		h3 := comLlamar(job, 3, uintptr(unsafe.Pointer(&estado)))
		if int32(h3) >= 0 {
			if estado.Completion == 3 || estado.JobStatus < 0 {
				liberar()
				return "", fmt.Errorf("Windows no pudo imprimir el XPS en «%s» (%s)", impresora, hrTexto(estado.JobStatus))
			}
		}
	}
	liberar()
	return "", nil
}

// ImprimirRAW manda bytes crudos (datatype RAW) a la impresora destino.
// Devuelve un detalle opcional cuando el trabajo quedó atascado en Windows.
func ImprimirRAW(nombreImpresora string, doc string, datos []byte) (string, error) {
	if nombreImpresora == "" {
		return "", errors.New("no hay impresora destino configurada")
	}
	var hPrint syscall.Handle
	r1, _, err := procOpenPrinterW.Call(uintptr(unsafe.Pointer(mustUTF16(nombreImpresora))),
		uintptr(unsafe.Pointer(&hPrint)), 0)
	if r1 == 0 {
		return "", fmt.Errorf("no se pudo abrir la impresora %q: %v", nombreImpresora, err)
	}
	di := docInfo1{
		DocName:    mustUTF16(doc),
		Datatype:   mustUTF16("RAW"),
		OutputFile: nil,
	}
	jobID, _, err := procStartDocPrinterW.Call(uintptr(hPrint), 1, uintptr(unsafe.Pointer(&di)))
	if jobID == 0 {
		procClosePrinter.Call(uintptr(hPrint))
		return "", fmt.Errorf("StartDoc fallo: %v", err)
	}
	if _, _, err = procStartPagePrinter.Call(uintptr(hPrint)); err.(syscall.Errno) != 0 {
		procEndDocPrinter.Call(uintptr(hPrint))
		procClosePrinter.Call(uintptr(hPrint))
		return "", fmt.Errorf("StartPage fallo: %v", err)
	}
	const chunk = 32768
	for off := 0; off < len(datos); off += chunk {
		end := off + chunk
		if end > len(datos) {
			end = len(datos)
		}
		esc := datos[off:end]
		var written uint32
		r2, _, e3 := procWritePrinter.Call(uintptr(hPrint),
			uintptr(unsafe.Pointer(&esc[0])), uintptr(len(esc)), uintptr(unsafe.Pointer(&written)))
		if r2 == 0 {
			procEndPagePrinter.Call(uintptr(hPrint))
			procEndDocPrinter.Call(uintptr(hPrint))
			procClosePrinter.Call(uintptr(hPrint))
			return "", fmt.Errorf("WritePrinter fallo: %v", e3)
		}
	}
	procEndPagePrinter.Call(uintptr(hPrint))
	ok, _, _ := procEndDocPrinter.Call(uintptr(hPrint))
	if ok == 0 {
		procClosePrinter.Call(uintptr(hPrint))
		return "", fmt.Errorf("EndDoc fallo (job %d)", jobID)
	}
	detalle := esperarTrabajo(hPrint, uint32(jobID))
	procClosePrinter.Call(uintptr(hPrint))
	return detalle, nil
}
