; Multi-segment + relocations: cross-segment abs refs, same-segment
; branch, <lo/>hi byte relocs, .word label relocs, .res in DATA.
.segment "CODE"
start:
        LDX #$00
loop:
        LDA msg,X
        BEQ done
        STA buf,X
        INX
        JMP loop
done:
        LDA #<msg
        LDX #>msg
        STA ptr
        STX ptr+1
        RTS
        .word start
        .word msg

.segment "RODATA"
msg:
        .byte $48, $49, $00

.segment "DATA"
buf:
        .res 4, $00
ptr:
        .res 2, $00
