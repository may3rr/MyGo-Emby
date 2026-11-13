import AppKit
import AVKit
import AVFoundation

let app = NSApplication.shared
app.setActivationPolicy(.regular)
let window = NSWindow(contentRect: NSRect(x: 250,y: 150,width: 960,height: 640),styleMask: [.borderless],backing: .buffered,defer: false)
window.backgroundColor = .black
window.level = .normal
let view = AVPlayerView(frame: window.contentView!.bounds)
view.autoresizingMask = [.width,.height]
view.controlsStyle = .none
let player = AVPlayer(url: URL(fileURLWithPath: CommandLine.arguments[1]))
player.isMuted = true
view.player = player
window.contentView = view
window.makeKeyAndOrderFront(nil)
app.activate(ignoringOtherApps: true)
player.play()
DispatchQueue.main.asyncAfter(deadline: .now()+2) {
    player.pause()
    let task = Process()
    task.executableURL = URL(fileURLWithPath: "/usr/sbin/screencapture")
    task.arguments = ["-x","-l",String(window.windowNumber),CommandLine.arguments[2]]
    try! task.run()
    task.waitUntilExit()
    print("reference capture status",task.terminationStatus)
    app.stop(nil)
    let ev = NSEvent.otherEvent(with: .applicationDefined,location:.zero,modifierFlags:[],timestamp:0,windowNumber:0,context:nil,subtype:0,data1:0,data2:0)!
    app.postEvent(ev,atStart:true)
}
app.run()
