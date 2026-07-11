// Builds the iCode backend binary used by the desktop (Electron) app.
//
// On Windows the backend is compiled as a GUI-subsystem executable
// (`-H windowsgui`) so that spawning it from Electron does NOT pop a console
// window on startup. On other platforms it is built as a normal binary.
const { execSync } = require('child_process');
const path = require('path');

const root = path.resolve(__dirname);
const isWin = process.platform === 'win32';
const out = isWin ? path.join('desktop', 'icode-server.exe') : path.join('desktop', 'icode-server');

let cmd = 'go build';
if (isWin) {
  cmd += ' -ldflags "-H windowsgui"';
}
cmd += ` -o ${JSON.stringify(out)} .`;

console.log('[build-desktop-backend]', cmd);
execSync(cmd, { stdio: 'inherit', cwd: root });
console.log('[build-desktop-backend] done ->', out);
