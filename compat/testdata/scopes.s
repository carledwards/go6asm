; .scope / .proc with lexical and ::-qualified name resolution.
g = $05

.scope LIB
val = $42
.endscope

.proc routine
    LDA #g            ; resolves outward to the global g
    LDX #LIB::val     ; qualified access into a sibling scope
inner:
    RTS
.endproc

    JMP routine
    JMP routine::inner
