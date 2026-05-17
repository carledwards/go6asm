; A minimal C64 program: loads at $0801, cross-segment relocs, no
; hardware vectors (the KERNAL owns $FFFA-$FFFF on a real C64).
.segment "CODE"
start:
        LDX #$00
copy:
        LDA src,X
        STA $0400,X        ; screen RAM
        INX
        CPX #msglen
        BNE copy
        RTS
        .word start        ; .word reloc into CODE
        .byte <src, >src   ; lo/hi byte relocs into RODATA

.segment "RODATA"
src:
        .byte $08, $05, $0C, $0C, $0F   ; "HELLO" in screen codes
msglen = 5

.segment "DATA"
scratch:
        .res 4, $00
